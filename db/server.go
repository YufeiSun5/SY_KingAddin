// server.go 提供 HTTP 层响应辅助与错误分类，路由由 Manager.RegisterRoutes 注册。
package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// ── 响应辅助 ─────────────────────────────────────────────────────────────────

// dbOK 统一成功响应，与 Python 版保持一致（success: true）。
func dbOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"data":    data,
	})
}

// dbFail 统一失败响应，HTTP 层统一 200（与 Python 版保持一致）。
func dbFail(w http.ResponseWriter, errType, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error": map[string]string{
			"errorType": errType,
			"message":   msg,
		},
	})
}

// corsOK 返回 CORS 预检响应。
func corsOK(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.WriteHeader(http.StatusNoContent)
}

// ── 辅助 ─────────────────────────────────────────────────────────────────────

// classifyError 将底层错误归类为对用户友好的错误类型标签。
// 对应 Python 版的 ProgrammingError / OperationalError / Exception 分支。
func classifyError(err error) string {
	if err == nil {
		return "未知错误"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case err == ErrNotConnected:
		return "数据库未连接"
	case strings.Contains(msg, "syntax") ||
		strings.Contains(msg, "you have an error in your sql syntax"):
		return "SQL语法错误"
	case strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "unknown column") ||
		strings.Contains(msg, "access denied") ||
		strings.Contains(msg, "table") && strings.Contains(msg, "exist"):
		return "数据库运行错误"
	case strings.Contains(msg, "connection") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "dial"):
		return "数据库连接错误"
	default:
		return "SQL执行错误"
	}
}

// truncateSQL 截断过长的 SQL 用于日志输出。
func truncateSQL(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// ── 批量执行 HTTP 接口 ───────────────────────────────────────────────────────

// handleBatchExec 处理批量 SQL 入队请求。
// 支持两种调用方式：
//  1. 纯文本模式（推荐，SCADA 友好）：
//     POST /api/db/batch-exec?conn=229   Body 为原始 SQL 文本（多条用分号分隔）
//  2. JSON 模式（网页/测试用）：
//     POST /api/db/batch-exec  Body: {"conn":"229","sqls":["INSERT ..."]}
//
// 队列满时阻塞等待（不返回错误），SQL 中的 NOW() 自动替换为入队时间。
// 返回 "OK" 或 "NG"。
func (m *Manager) handleBatchExec(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprint(w, "NG")
		return
	}
	if m.batch == nil {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprint(w, "NG")
		return
	}

	rawBody, _ := io.ReadAll(r.Body)
	if len(rawBody) == 0 {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprint(w, "NG")
		return
	}

	var conn string
	var sqls []string

	// 判断是 JSON 还是纯文本：以 { 开头视为 JSON，否则纯文本
	trimmed := bytes.TrimSpace(rawBody)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		// JSON 模式（标准双引号 JSON）
		var body struct {
			SQLs []string `json:"sqls"`
			Conn string   `json:"conn"`
		}
		if err := json.Unmarshal(rawBody, &body); err != nil || len(body.SQLs) == 0 {
			log.Printf("[batch-exec] ❌ JSON解析失败: %v", err)
			w.Header().Set("Access-Control-Allow-Origin", "*")
			fmt.Fprint(w, "NG")
			return
		}
		conn = body.Conn
		sqls = body.SQLs
	} else {
		// 纯文本模式：conn 从 URL 参数取，body 就是 SQL 原文（多条用分号分隔）
		conn = r.URL.Query().Get("conn")
		raw := strings.TrimSpace(string(rawBody))
		for _, s := range strings.Split(raw, ";") {
			s = strings.TrimSpace(s)
			if s != "" {
				sqls = append(sqls, s)
			}
		}
	}

	if len(sqls) == 0 {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprint(w, "NG")
		return
	}

	log.Printf("[batch-exec] 连接=%s, %d条SQL入队", conn, len(sqls))

	if _, err := m.batch.Enqueue(r.Context(), conn, sqls); err != nil {
		log.Printf("[batch-exec] ❌ 入队失败: %v", err)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprint(w, "NG")
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprint(w, "OK")
}

// handleBatchStatus 返回所有连接的批量队列统计。
// GET /api/db/batch-status
func (m *Manager) handleBatchStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodGet {
		dbFail(w, "请求方法错误", "仅支持 GET")
		return
	}
	if m.batch == nil {
		dbFail(w, "服务未就绪", "批量执行器未初始化")
		return
	}

	dbOK(w, map[string]any{"queues": m.batch.AllStats()})
}

// handleBatchDead 返回指定连接的死信列表（内存中最近 N 条）。
// GET /api/db/batch-dead?conn=连接名
func (m *Manager) handleBatchDead(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodGet {
		dbFail(w, "请求方法错误", "仅支持 GET")
		return
	}
	if m.batch == nil {
		dbFail(w, "服务未就绪", "批量执行器未初始化")
		return
	}

	connName := r.URL.Query().Get("conn")
	if connName == "" {
		connName = "default"
	}

	dbOK(w, map[string]any{"dead_letters": m.batch.DeadLetters(connName)})
}
