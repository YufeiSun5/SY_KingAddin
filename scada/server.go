// server.go 将 ScadaClient 的能力暴露为 HTTP JSON 接口。
// 对应 Python scada_router.py，供 Postman / 网页插件直接调用。
//
// 路由一览：
//   GET  /api/scada/status            连接状态快照
//   POST /api/scada/refresh-token     强制刷新 Token
//   GET  /api/scada/variables         获取变量列表
//   POST /api/scada/read              批量读取实时值
//   POST /api/scada/write             批量写入变量值

package scada

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// ── 响应辅助 ─────────────────────────────────────────────────────────────────

// reply 统一写 JSON 响应。
func reply(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// ok 200 成功响应。
func ok(w http.ResponseWriter, data any) {
	reply(w, http.StatusOK, map[string]any{"code": 0, "data": data})
}

// fail 200 业务失败响应（与 Python 保持一致，HTTP 层统一 200）。
func fail(w http.ResponseWriter, msg string) {
	reply(w, http.StatusOK, map[string]any{"code": -1, "message": msg})
}

// ── 路由注册 ─────────────────────────────────────────────────────────────────

// RegisterRoutes 将所有 SCADA 路由挂载到 mux 上。
// prefix 通常为 "/api/scada"。
func (c *Client) RegisterRoutes(mux *http.ServeMux, prefix string) {
	// CORS 预检
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc(prefix+"/status", c.handleStatus)
	mux.HandleFunc(prefix+"/refresh-token", c.handleRefreshToken)
	mux.HandleFunc(prefix+"/variables", c.handleGetVariables)
	mux.HandleFunc(prefix+"/read", c.handleRead)
	mux.HandleFunc(prefix+"/write", c.handleWrite)
}

// NewServer 创建并返回一个 *http.Server，已挂载所有 SCADA 路由。
// addr 示例: ":8004"
func (c *Client) NewServer(addr string) *http.Server {
	mux := http.NewServeMux()
	c.RegisterRoutes(mux, "/api/scada")
	return &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// ── 处理函数 ─────────────────────────────────────────────────────────────────

// GET /api/scada/status
// 返回连接状态快照，不发出任何 SCADA 请求。
func (c *Client) handleStatus(w http.ResponseWriter, r *http.Request) {
	corsHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		fail(w, "仅支持 GET")
		return
	}
	ok(w, c.GetConnectionStatus())
}

// POST /api/scada/refresh-token
// 强制刷新 Token（先删旧的，再取新的）。
func (c *Client) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	corsHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		fail(w, "仅支持 POST")
		return
	}
	if err := c.Login(true); err != nil {
		log.Printf("❌ 强制刷新 Token 失败: %v", err)
		fail(w, err.Error())
		return
	}
	ok(w, c.GetConnectionStatus())
}

// GET /api/scada/variables
// 从 SCADA 拉取变量列表（带指数退避重试）。
func (c *Client) handleGetVariables(w http.ResponseWriter, r *http.Request) {
	corsHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		fail(w, "仅支持 GET")
		return
	}
	vars, err := c.GetVariables()
	if err != nil {
		log.Printf("❌ 获取变量列表失败: %v", err)
		fail(w, err.Error())
		return
	}
	reply(w, http.StatusOK, map[string]any{
		"code":  0,
		"count": len(vars),
		"data":  vars,
	})
}

// POST /api/scada/read
// 请求体: {"tags": ["Tag1", "Tag2"]}
// 批量读取实时值。
func (c *Client) handleRead(w http.ResponseWriter, r *http.Request) {
	corsHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		fail(w, "仅支持 POST")
		return
	}

	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Tags) == 0 {
		fail(w, "请求体格式错误，需要 {\"tags\": [\"Tag1\",...]}")
		return
	}

	vals, err := c.ReadVariables(body.Tags)
	if err != nil {
		log.Printf("❌ 读取变量失败: %v", err)
		fail(w, err.Error())
		return
	}
	ok(w, vals)
}

// POST /api/scada/write
// 请求体: {"items": [{"name": "Tag1", "value": 3.14}]}
// 批量写入变量值。
func (c *Client) handleWrite(w http.ResponseWriter, r *http.Request) {
	corsHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		fail(w, "仅支持 POST")
		return
	}

	var body struct {
		Items []WriteItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Items) == 0 {
		fail(w, "请求体格式错误，需要 {\"items\": [{\"N\":\"Tag1\",\"V\":\"值\"}]}")
		return
	}

	if err := c.WriteVariables(body.Items); err != nil {
		log.Printf("❌ 写入变量失败: %v", err)
		fail(w, err.Error())
		return
	}
	reply(w, http.StatusOK, map[string]any{
		"code":    0,
		"message": "写入成功",
		"count":   len(body.Items),
	})
}

// corsHeaders 统一添加跨域响应头。
func corsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}
