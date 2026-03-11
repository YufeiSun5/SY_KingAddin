// Package db 多连接管理器：按名称管理多个 Pool，支持列表、重连。
package db

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
)

// Manager 管理多个命名数据库连接，每条连接独立 watchdog 断线重连。
type Manager struct {
	mu    sync.RWMutex
	pools map[string]*Pool
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

// Close 关闭所有连接并停止所有 watchdog。
func (m *Manager) Close() {
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
	var body struct {
		SQL  string `json:"sql"`
		Conn string `json:"conn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SQL) == "" {
		dbFail(w, "请求格式错误", `请求体必须为 {"sql": "SELECT ..."}，可选 "conn": "连接名"`)
		return
	}
	sqlStr := strings.TrimSpace(body.SQL)
	pool := m.poolByName(body.Conn)
	if pool == nil {
		dbFail(w, "数据库未连接", "无可用连接，请检查配置或等待重连")
		return
	}

	// KH 工业库模式：整段 SQL 一次发送给驱动，由驱动解析多条语句
	if pool.IsKH() {
		result, err := pool.RunKH(sqlStr)
		if err != nil {
			log.Printf("[db] KH 查询失败 [%s]: %v | sql: %s", body.Conn, err, truncateSQL(sqlStr))
			dbFail(w, classifyError(err), err.Error())
			return
		}
		log.Printf("[db] KH 查询成功 [%s]: %s", body.Conn, truncateSQL(sqlStr))
		dbOK(w, result)
		return
	}

	upper := strings.ToUpper(sqlStr)
	isQuery := strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "WITH") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "EXPLAIN")
	if isQuery {
		result, err := pool.RunSQL(sqlStr)
		if err != nil {
			log.Printf("[db] 查询失败 [%s]: %v | sql: %s", body.Conn, err, truncateSQL(sqlStr))
			dbFail(w, classifyError(err), err.Error())
			return
		}
		log.Printf("[db] 查询成功 [%s]: %s", body.Conn, truncateSQL(sqlStr))
		dbOK(w, result)
	} else {
		result, err := pool.ExecSQL(sqlStr)
		if err != nil {
			log.Printf("[db] 执行失败 [%s]: %v | sql: %s", body.Conn, err, truncateSQL(sqlStr))
			dbFail(w, classifyError(err), err.Error())
			return
		}
		log.Printf("[db] 执行成功 [%s]，影响行数: %d | sql: %s", body.Conn, result.AffectedRows, truncateSQL(sqlStr))
		dbOK(w, result)
	}
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
