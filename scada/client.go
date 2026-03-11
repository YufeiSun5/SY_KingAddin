// Package scada 封装 SCADA RESTful API 客户端，实现 Token 管理与指数退避重试。
package scada

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ── 常量 ────────────────────────────────────────────────────────────────────

const (
	tokenLifetime      = 60 * time.Minute // Token 有效期
	tokenRefreshBefore = 5 * time.Minute  // 提前多久主动刷新
	tokenTickInterval  = 50 * time.Minute // 主动刷新 Ticker 间隔

	reconnectCooldown = 60 * time.Second // 断连后最小重连间隔

	backoffInit = 1 * time.Second  // 指数退避初始间隔
	backoffMax  = 32 * time.Second // 指数退避上限

	maxConcurrent  = 5  // 最大并发请求数
	maxTimeoutHits = 3  // 连续超时阈值，超过后强制重连
	httpTimeout    = 10 * time.Second
)

// ── 配置 ────────────────────────────────────────────────────────────────────

// Config 保存 SCADA 连接所需的全部参数。
type Config struct {
	BaseURL          string // http://host:port/api/v1
	Username         string
	Password         string
	LocalCallbackURL string // 本机回调根地址，如 http://192.168.x.x:8004
}

// ── 状态类型 ─────────────────────────────────────────────────────────────────

// TokenStatus 对外暴露当前 Token 的生命周期信息。
type TokenStatus struct {
	HasToken        bool    `json:"has_token"`
	IsExpired       bool    `json:"is_expired"`
	ShouldRefresh   bool    `json:"should_refresh"`
	ElapsedSeconds  float64 `json:"elapsed_seconds,omitempty"`
	RemainingSeconds float64 `json:"remaining_seconds,omitempty"`
}

// ConnectionStatus 对外暴露连接整体状态。
type ConnectionStatus struct {
	IsConnected       bool        `json:"is_connected"`
	HasToken          bool        `json:"has_token"`
	TokenPreview      string      `json:"token_preview,omitempty"`
	TokenStatus       TokenStatus `json:"token_status"`
	TokenAcquiredAt   string      `json:"token_acquired_at,omitempty"`
	TokenExpiresAt    string      `json:"token_expires_at,omitempty"`
	TokenRefreshAt    string      `json:"token_refresh_at,omitempty"`
	CooldownSeconds   float64     `json:"cooldown_seconds"`
	ReconnectAvailable bool       `json:"reconnect_available"`
	CooldownRemaining float64     `json:"cooldown_remaining"`
}

// ── 核心结构体 ────────────────────────────────────────────────────────────────

// Client 是 SCADA HTTP 客户端，线程安全。
type Client struct {
	cfg Config

	// Token 与连接状态（使用读写锁保护）
	mu              sync.RWMutex
	token           string
	tokenAcquiredAt time.Time // 零值表示无 Token
	isConnected     bool
	lastDisconnect  time.Time // 零值表示未曾断连

	// 并发保护标志（atomic 避免额外锁）
	refreshing atomic.Bool // 正在刷新 Token

	// 超时计数（仅在请求 goroutine 内访问，已受信号量保护）
	timeoutCount int

	// HTTP 客户端（连接池复用）
	http *http.Client

	// 并发信号量
	sem chan struct{}

	// 生命周期控制
	ctx    context.Context
	cancel context.CancelFunc
}

// ── 构造与启动 ────────────────────────────────────────────────────────────────

// New 创建并返回一个已初始化的 Client，调用方需调用 Start() 启动后台协程。
func New(cfg Config) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		cfg:    cfg,
		sem:    make(chan struct{}, maxConcurrent),
		ctx:    ctx,
		cancel: cancel,
		http: &http.Client{
			Timeout: httpTimeout,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
	return c
}

// Start 启动主动 Token 刷新定时协程，应在应用启动时调用一次。
func (c *Client) Start() {
	go c.tokenRefreshLoop()
}

// Close 清理 Token 并关闭客户端，应在应用退出时调用。
// 顺序：先释放远端 Token（需要网络），再取消 ctx（停止后台协程）。
func (c *Client) Close() {
	// 先取出 Token（持读锁避免竞态）
	c.mu.RLock()
	tok := c.token
	c.mu.RUnlock()

	// 用独立的带超时 context 发送 DelToken，避免被主 ctx 提前取消
	if tok != "" {
		delCtx, delCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer delCancel()
		// 临时借用 http 客户端直接发请求，不走依赖主 ctx 的 doPOST
		url := c.cfg.BaseURL + "/DelToken"
		req, err := http.NewRequestWithContext(delCtx, http.MethodPost, url, nil)
		if err == nil {
			req.Header.Set("token", tok)
			resp, err := c.http.Do(req)
			if err != nil {
				log.Printf("⚠️ 关闭时删除 Token 失败: %v", err)
			} else {
				resp.Body.Close()
				log.Printf("✅ 关闭时 Token 已释放: %s...", tok[:min8(len(tok))])
			}
		}
		// 清空本地记录
		c.mu.Lock()
		c.token = ""
		c.tokenAcquiredAt = time.Time{}
		c.mu.Unlock()
	}

	// 最后取消 ctx，停止 tokenRefreshLoop 等后台协程
	c.cancel()
}

// ── Token 生命周期 ─────────────────────────────────────────────────────────

// tokenRefreshLoop 每 50 分钟主动触发一次 Token 刷新。
func (c *Client) tokenRefreshLoop() {
	ticker := time.NewTicker(tokenTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			log.Println("⏰ Token 定时刷新触发...")
			_ = c.Login(false)
		}
	}
}

// tokenStatus 返回当前 Token 状态（调用方持有读锁）。
func (c *Client) tokenStatus() TokenStatus {
	if c.token == "" || c.tokenAcquiredAt.IsZero() {
		return TokenStatus{ShouldRefresh: true, IsExpired: true}
	}
	elapsed := time.Since(c.tokenAcquiredAt)
	remaining := tokenLifetime - elapsed
	return TokenStatus{
		HasToken:         true,
		IsExpired:        elapsed >= tokenLifetime,
		ShouldRefresh:    remaining <= tokenRefreshBefore,
		ElapsedSeconds:   elapsed.Seconds(),
		RemainingSeconds: max0(remaining.Seconds()),
	}
}

// shouldRefresh 判断是否需要刷新 Token（需先持有读锁）。
func (c *Client) shouldRefresh() bool {
	st := c.tokenStatus()
	return st.ShouldRefresh
}

// getToken 返回当前 Token，必要时等待刷新完成。
func (c *Client) getToken() (string, error) {
	c.mu.RLock()
	need := c.shouldRefresh()
	c.mu.RUnlock()

	if need {
		if !c.refreshing.Load() {
			// 当前 goroutine 负责刷新
			if err := c.Login(false); err != nil {
				return "", err
			}
		} else {
			// 等待其他 goroutine 完成刷新（最多 10 秒）
			deadline := time.Now().Add(10 * time.Second)
			for c.refreshing.Load() && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	c.mu.RLock()
	tok := c.token
	c.mu.RUnlock()
	if tok == "" {
		return "", fmt.Errorf("Token 为空，SCADA 未连接")
	}
	return tok, nil
}

// markDisconnected 记录断连时刻（幂等）。
func (c *Client) markDisconnected() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isConnected {
		c.isConnected = false
		c.lastDisconnect = time.Now()
		log.Printf("🔌 SCADA 连接断开，%s 后可重连", reconnectCooldown)
	}
}

// ── 网络辅助 ──────────────────────────────────────────────────────────────────

// testTCP 检测 SCADA 服务器 TCP 连通性。
func (c *Client) testTCP() bool {
	addr := hostPort(c.cfg.BaseURL)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Printf("❌ TCP 连通性测试失败: %v", err)
		return false
	}
	conn.Close()
	log.Printf("✅ TCP 连通: %s", addr)
	return true
}

// acquire 获取并发令牌。
func (c *Client) acquire() { c.sem <- struct{}{} }

// release 释放并发令牌。
func (c *Client) release() { <-c.sem }

// ── 指数退避 ──────────────────────────────────────────────────────────────────

// retryWithBackoff 对 operation 执行指数退避重试；isCritical 返回 true 时先刷新 Token。
// 当 ctx 取消后立即退出循环。
func (c *Client) retryWithBackoff(operation func() error, isCritical func(error) bool) error {
	backoff := backoffInit
	for {
		err := operation()
		if err == nil {
			return nil
		}
		// 判断是否需要先刷新 Token
		if isCritical != nil && isCritical(err) {
			log.Printf("⚠️ 关键错误，触发 Token 刷新: %v", err)
			_ = c.Login(true)
		}
		log.Printf("🔄 退避等待 %s 后重试: %v", backoff, err)
		select {
		case <-c.ctx.Done():
			return fmt.Errorf("客户端已关闭: %w", err)
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// ── 公共 API ──────────────────────────────────────────────────────────────────

// GetConnectionStatus 返回当前连接与 Token 的完整状态快照。
func (c *Client) GetConnectionStatus() ConnectionStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	st := c.tokenStatus()
	status := ConnectionStatus{
		IsConnected:     c.isConnected,
		HasToken:        c.token != "",
		TokenStatus:     st,
		CooldownSeconds: reconnectCooldown.Seconds(),
	}
	if c.token != "" && len(c.token) >= 8 {
		status.TokenPreview = c.token[:8] + "..."
	}
	if !c.tokenAcquiredAt.IsZero() {
		layout := "2006-01-02 15:04:05"
		status.TokenAcquiredAt = c.tokenAcquiredAt.Format(layout)
		status.TokenExpiresAt = c.tokenAcquiredAt.Add(tokenLifetime).Format(layout)
		status.TokenRefreshAt = c.tokenAcquiredAt.Add(tokenLifetime - tokenRefreshBefore).Format(layout)
	}
	if c.lastDisconnect.IsZero() {
		status.ReconnectAvailable = true
	} else {
		remaining := reconnectCooldown - time.Since(c.lastDisconnect)
		if remaining < 0 {
			remaining = 0
		}
		status.CooldownRemaining = remaining.Seconds()
		status.ReconnectAvailable = remaining == 0
	}
	return status
}

// ── 内部工具 ──────────────────────────────────────────────────────────────────

// scadaResp 是 SCADA 通用响应包装。
type scadaResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Token   string          `json:"token,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Nums    int             `json:"nums,omitempty"`
}

// doGET 执行 GET 请求并解析响应。
func (c *Client) doGET(url string, params map[string]string, headers map[string]string) (*scadaResp, error) {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.execRequest(req)
}

// doPOST 将 payload 序列化为 JSON，以 UTF-8 编码发送 POST 请求。
func (c *Client) doPOST(url string, payload any, headers map[string]string) (*scadaResp, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.execRequest(req)
}

// execRequest 执行 HTTP 请求，处理超时计数与连接错误。
func (c *Client) execRequest(req *http.Request) (*scadaResp, error) {
	c.acquire()
	defer c.release()

	resp, err := c.http.Do(req)
	if err != nil {
		// 判断是否为超时
		if isTimeout(err) {
			c.timeoutCount++
			log.Printf("❌ 请求超时 (%d/%d): %v", c.timeoutCount, maxTimeoutHits, err)
			if c.timeoutCount >= maxTimeoutHits {
				log.Printf("⚠️ 连续超时 %d 次，标记断连", c.timeoutCount)
				c.markDisconnected()
				c.timeoutCount = 0
			}
		} else {
			c.markDisconnected()
		}
		return nil, err
	}
	defer resp.Body.Close()

	// 重置超时计数
	c.timeoutCount = 0
	if !c.isConnected {
		c.mu.Lock()
		c.isConnected = true
		c.lastDisconnect = time.Time{}
		c.mu.Unlock()
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	log.Printf("[%s %s] → %d  body=%s", req.Method, req.URL.Path, resp.StatusCode, truncate(string(raw), 200))

	if resp.StatusCode == 502 {
		c.markDisconnected()
		return nil, fmt.Errorf("SCADA 返回 502 Bad Gateway")
	}
	if resp.StatusCode != 200 {
		c.markDisconnected()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}

	var r scadaResp
	if err = json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("响应非 JSON (status=%d): %s", resp.StatusCode, raw)
	}
	return &r, nil
}

// ── 辅助函数 ──────────────────────────────────────────────────────────────────

func max0(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}

// hostPort 从 base_url（如 http://host:9433/api/v1）提取 host:port。
func hostPort(baseURL string) string {
	// 去掉协议头
	s := baseURL
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			s = s[len(prefix):]
			break
		}
	}
	// 取 host:port 部分（去掉路径）
	for i, ch := range s {
		if ch == '/' {
			return s[:i]
		}
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// isTimeout 判断错误是否属于超时类型。
func isTimeout(err error) bool {
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout()
	}
	return false
}

// isTokenInvalid 判断 SCADA 业务层 Token 失效（code == -5）。
func isTokenInvalid(r *scadaResp) bool {
	return r != nil && r.Code == -5
}
