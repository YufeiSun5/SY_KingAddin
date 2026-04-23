// Package db 多连接管理器：按名称管理多个 Pool，支持列表、重连。
package db

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
)

const legacyDefaultConnName = "229"

type runSQLRequest struct {
	SQL  string `json:"sql"`
	Conn string `json:"conn"`
}

// Manager 管理多个命名数据库连接，每条连接独立 watchdog 断线重连。
type Manager struct {
	mu    sync.RWMutex
	pools map[string]*Pool
	batch *BatchExecutor // 批量 SQL 执行器（可选，通过 StartBatch 初始化）
}

// NewManager 根据配置创建多条连接并启动各自的 watchdog。
func NewManager(configs []ConnConfig) *Manager {
	m := &Manager{pools: make(map[string]*Pool)}
	for i := range configs {
		cfg := configs[i]
		cfg.Normalize()
		name := cfg.Name
		if name == "" {
			name = "default"
		}
		// 重名则覆盖（同名单条）
		if old := m.pools[name]; old != nil {
			old.Close()
		}
		m.pools[name] = New(cfg)
	}
	return m
}

// GetPool 按名称获取连接池，不存在返回 nil。
func (m *Manager) GetPool(name string) *Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pools[name]
}

// ListStatus 返回所有连接的状态列表，供前端与 HTTP 展示。
func (m *Manager) ListStatus() []ConnStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ConnStatus, 0, len(m.pools))
	for _, p := range m.pools {
		out = append(out, p.GetStatus())
	}
	return out
}

// Reconnect 断开指定连接并立即按原配置重新建连（watchdog 会继续保活）。
func (m *Manager) Reconnect(name string) error {
	m.mu.Lock()
	p := m.pools[name]
	if p == nil {
		m.mu.Unlock()
		return nil // 无此连接，视为成功
	}
	cfg := p.Config()
	p.Close()
	m.pools[name] = New(cfg)
	m.mu.Unlock()
	log.Printf("[db] 已触发重连: %s", name)
	return nil
}

// StartBatch 初始化批量 SQL 执行器，必须在 NewManager 之后调用。
func (m *Manager) StartBatch(cfg BatchConfig, dataDir string) {
	m.batch = NewBatchExecutor(cfg, m, dataDir)
}

// Close 关闭所有连接并停止所有 watchdog。
// 批量执行器先于连接池关闭（排空队列需要数据库连接）。
func (m *Manager) Close() {
	if m.batch != nil {
		m.batch.Close()
		m.batch = nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, p := range m.pools {
		p.Close()
		delete(m.pools, name)
	}
	log.Printf("[db] 多连接管理器已关闭")
}

// defaultPool 返回默认连接（名为 default 或第一个）。
func (m *Manager) defaultPool() *Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p := m.pools["default"]; p != nil {
		return p
	}
	for _, p := range m.pools {
		return p
	}
	return nil
}

// poolByName 按名称取连接，空名用默认。
func (m *Manager) poolByName(name string) *Pool {
	if name == "" {
		return m.defaultPool()
	}
	return m.GetPool(name)
}

// RegisterRoutes 将多连接下的数据库路由挂载到 mux。
// POST /api/db/run-sql 请求体可带 "conn": "连接名" 指定数据源，缺省用默认连接。
// GET  /api/db/status 返回所有连接状态列表。
func (m *Manager) RegisterRoutes(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			corsOK(w)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc(prefix+"/run-sql", m.handleRunSQL)
	mux.HandleFunc(prefix+"/status", m.handleStatus)
	mux.HandleFunc(prefix+"/batch-exec", m.handleBatchExec)
	mux.HandleFunc(prefix+"/batch-status", m.handleBatchStatus)
	mux.HandleFunc(prefix+"/batch-dead", m.handleBatchDead)
}

// RegisterLegacyRoutes 注册旧版网页仍在使用的兼容数据库路由。
func (m *Manager) RegisterLegacyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/sql/run-sql", m.handleRunSQLLegacy)
}

func decodeRunSQLRequest(r *http.Request) (runSQLRequest, error) {
	var body runSQLRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SQL) == "" {
		return runSQLRequest{}, err
	}
	body.SQL = strings.TrimSpace(body.SQL)
	return body, nil
}

func isQuerySQL(sqlStr string) bool {
	upper := strings.ToUpper(sqlStr)
	return strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "WITH") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "EXPLAIN")
}

func (m *Manager) executeRunSQL(connName, sqlStr string) (any, bool, error) {
	pool := m.poolByName(connName)
	if pool == nil {
		return nil, false, ErrNotConnected
	}

	if pool.IsKH() {
		result, err := pool.RunKH(sqlStr)
		return result, true, err
	}

	if isQuerySQL(sqlStr) {
		result, err := pool.RunSQL(sqlStr)
		return result, true, err
	}

	result, err := pool.ExecSQL(sqlStr)
	return result, false, err
}

func logRunSQLResult(connName, sqlStr string, isQuery bool, err error) {
	if err != nil {
		if isQuery {
			log.Printf("[db] 查询失败 [%s]: %v | sql: %s", connName, err, truncateSQL(sqlStr))
			return
		}
		log.Printf("[db] 执行失败 [%s]: %v | sql: %s", connName, err, truncateSQL(sqlStr))
		return
	}

	if isQuery {
		log.Printf("[db] 查询成功 [%s]: %s", connName, truncateSQL(sqlStr))
		return
	}
	log.Printf("[db] 执行成功 [%s]: %s", connName, truncateSQL(sqlStr))
}

func (m *Manager) resolveLegacyConnName(connName string) string {
	connName = strings.TrimSpace(connName)
	if connName != "" {
		return connName
	}
	if m.GetPool(legacyDefaultConnName) != nil {
		return legacyDefaultConnName
	}
	if p := m.defaultPool(); p != nil {
		return p.Config().Name
	}
	return connName
}

func legacyRunSQLReply(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	payload := map[string]any{
		"success": true,
		"code":    0,
	}

	switch v := result.(type) {
	case QueryResult:
		payload["data"] = v.Rows
		payload["rows"] = v.Rows
		payload["columns"] = v.Columns
		payload["count"] = v.Count
	case ExecResult:
		payload["data"] = map[string]any{
			"affected_rows":  v.AffectedRows,
			"last_insert_id": v.LastInsertID,
		}
		payload["affected_rows"] = v.AffectedRows
		payload["last_insert_id"] = v.LastInsertID
		payload["count"] = 0
	default:
		payload["data"] = result
	}

	_ = json.NewEncoder(w).Encode(payload)
}

func legacyRunSQLFail(w http.ResponseWriter, errType, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"code":    -1,
		"message": msg,
		"error": map[string]string{
			"errorType": errType,
			"message":   msg,
		},
	})
}

func (m *Manager) handleRunSQL(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodPost {
		dbFail(w, "请求方法错误", "仅支持 POST")
		return
	}
	body, err := decodeRunSQLRequest(r)
	if err != nil {
		dbFail(w, "请求格式错误", `请求体必须为 {"sql": "SELECT ..."}，可选 "conn": "连接名"`)
		return
	}
	result, isQuery, err := m.executeRunSQL(body.Conn, body.SQL)
	logRunSQLResult(body.Conn, body.SQL, isQuery, err)
	if err != nil {
		dbFail(w, classifyError(err), err.Error())
		return
	}
	dbOK(w, result)
}

func (m *Manager) handleRunSQLLegacy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodPost {
		legacyRunSQLFail(w, "请求方法错误", "仅支持 POST")
		return
	}
	body, err := decodeRunSQLRequest(r)
	if err != nil {
		legacyRunSQLFail(w, "请求格式错误", `请求体必须为 {"sql": "SELECT ..."}，可选 "conn": "连接名"`)
		return
	}
	body.Conn = m.resolveLegacyConnName(body.Conn)
	result, isQuery, err := m.executeRunSQL(body.Conn, body.SQL)
	logRunSQLResult(body.Conn, body.SQL, isQuery, err)
	if err != nil {
		legacyRunSQLFail(w, classifyError(err), err.Error())
		return
	}
	legacyRunSQLReply(w, result)
}

func (m *Manager) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodGet {
		dbFail(w, "请求方法错误", "仅支持 GET")
		return
	}
	dbOK(w, map[string]any{"connections": m.ListStatus()})
}
