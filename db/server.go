// server.go 提供 HTTP 层响应辅助与错误分类，路由由 Manager.RegisterRoutes 注册。
package db

import (
	"encoding/json"
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
