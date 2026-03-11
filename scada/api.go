// api.go 实现 SCADA 四个核心业务方法：
//   - Login        更新/刷新 Token
//   - GetVariables 获取变量列表
//   - ReadVariables 读取变量实时值
//   - WriteVariables 写入变量值
//   - DeleteToken  删除远端 Token（Login 内部调用，Close 时也调用）

package scada

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// ── 数据类型 ──────────────────────────────────────────────────────────────────

// Variable 单个变量描述（GetVariables 返回列表元素）。
// 字段名与 SCADA 原始响应一致：N=变量名, G=分组, D=描述/DSN。
type Variable struct {
	N string `json:"N"` // 变量名
	G string `json:"G"` // 分组
	D string `json:"D"` // 描述或 DSN
}

// VarValue 读取实时值时的单条结果。
// 字段名与 SCADA 原始响应一致：N=名称, V=值, Q=质量码, T=时间戳, Err=错误信息。
type VarValue struct {
	N   string `json:"N"`            // 变量名
	V   string `json:"V"`            // 值（SCADA 返回字符串）
	Q   int    `json:"Q,omitempty"`  // 质量码（192 = 正常）
	T   string `json:"T,omitempty"`  // 时间戳
	Err string `json:"Err,omitempty"` // 错误信息
}

// WriteItem 写入一个变量所需的键值对。
// N=变量名, V=值（字符串）, T=时间戳（可选）。
type WriteItem struct {
	N string `json:"N"` // 变量名
	V string `json:"V"` // 值
	T string `json:"T,omitempty"` // 时间戳
}

// ── Login（更新 Token）────────────────────────────────────────────────────────

// Login 登录 SCADA 获取新 Token。
//   - force=false：如果当前处于冷却期则跳过，并发时等待其他协程完成
//   - force=true ：忽略冷却期，直接强制重新获取（用于被动 Token 失效重试）
func (c *Client) Login(force bool) error {
	// 并发保护：已有刷新任务时根据 force 决定是否等待
	if !c.refreshing.CompareAndSwap(false, true) {
		if force {
			// 强制模式：等待已有刷新完成后再次发起
			for c.refreshing.Load() {
				time.Sleep(100 * time.Millisecond)
			}
			// 已被其他 goroutine 刷新完毕，不必重复
			return nil
		}
		log.Println("Token 刷新中，跳过本次请求")
		return nil
	}
	defer c.refreshing.Store(false)

	// 非强制模式下检查重连冷却
	if !force {
		c.mu.RLock()
		lastDis := c.lastDisconnect
		c.mu.RUnlock()
		if !lastDis.IsZero() {
			remaining := reconnectCooldown - time.Since(lastDis)
			if remaining > 0 {
				log.Printf("⏰ 重连冷却中，还需 %.1fs", remaining.Seconds())
				return fmt.Errorf("重连冷却: %.1fs 后可重试", remaining.Seconds())
			}
		}
	}

	// 先测网络连通性
	c.testTCP()

	// 如果已有旧 Token，先删除（避免远端 Token 超限）
	c.mu.RLock()
	oldToken := c.token
	c.mu.RUnlock()
	if oldToken != "" {
		log.Printf("🗑️ 删除旧 Token %s...", oldToken[:min8(len(oldToken))])
		if err := c.DeleteToken(oldToken); err != nil {
			log.Printf("⚠️ 删除旧 Token 失败（继续获取新 Token）: %v", err)
		}
	}

	// 发起登录请求
	url := c.cfg.BaseURL + "/GetToken"
	resp, err := c.doGET(url, map[string]string{
		"username": c.cfg.Username,
		"password": c.cfg.Password,
	}, nil)
	if err != nil {
		c.markDisconnected()
		return fmt.Errorf("GetToken 请求失败: %w", err)
	}
	if resp.Code != 0 {
		c.markDisconnected()
		return fmt.Errorf("GetToken 业务错误 code=%d: %s", resp.Code, resp.Message)
	}

	// 写入新 Token（持写锁）
	c.mu.Lock()
	c.token = resp.Token
	c.tokenAcquiredAt = time.Now()
	c.isConnected = true
	c.lastDisconnect = time.Time{}
	c.mu.Unlock()

	log.Printf("✅ Token 获取成功: %s...  有效期 %s，将在 %s 自动刷新",
		resp.Token[:min8(len(resp.Token))],
		tokenLifetime,
		time.Now().Add(tokenLifetime-tokenRefreshBefore).Format("15:04:05"),
	)
	return nil
}

// ── DeleteToken ───────────────────────────────────────────────────────────────

// DeleteToken 通知 SCADA 服务端释放指定 Token。
func (c *Client) DeleteToken(tok string) error {
	url := c.cfg.BaseURL + "/DelToken"
	resp, err := c.doPOST(url, nil, map[string]string{"token": tok})
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("DelToken 失败 code=%d: %s", resp.Code, resp.Message)
	}
	log.Printf("🗑️ Token 已删除，当前活动 Token 数: %d", resp.Nums)

	// 如果删的是当前持有的 Token，顺手清空本地记录
	c.mu.Lock()
	if c.token == tok {
		c.token = ""
		c.tokenAcquiredAt = time.Time{}
	}
	c.mu.Unlock()
	return nil
}

// ── GetVariables（获取变量列表）───────────────────────────────────────────────

// GetVariables 从 SCADA 拉取完整变量列表。
// 网络或 Token 错误时触发指数退避重试直至成功或 ctx 取消。
func (c *Client) GetVariables() ([]Variable, error) {
	url := c.cfg.BaseURL + "/GetVariables"
	var result []Variable

	err := c.retryWithBackoff(func() error {
		tok, e := c.getToken()
		if e != nil {
			return e
		}
		resp, e := c.doPOST(url, nil, map[string]string{"token": tok})
		if e != nil {
			return e
		}
		// Token 失效，标记为关键错误，由 isCritical 触发刷新
		if isTokenInvalid(resp) {
			return fmt.Errorf("TOKEN_INVALID: %s", resp.Message)
		}
		if resp.Code != 0 {
			return fmt.Errorf("GetVariables 业务错误 code=%d: %s", resp.Code, resp.Message)
		}
		if e = json.Unmarshal(resp.Data, &result); e != nil {
			return fmt.Errorf("解析变量列表失败: %w", e)
		}
		return nil
	}, isTokenErr)

	if err != nil {
		return nil, err
	}
	log.Printf("✅ 变量列表同步完成，共 %d 个变量", len(result))
	return result, nil
}

// ── ReadVariables（读取实时值）────────────────────────────────────────────────

// ReadVariables 批量读取变量实时值。
//
//	tagNames: 变量名列表，如 ["Tag1", "Tag2"]
func (c *Client) ReadVariables(tagNames []string) ([]VarValue, error) {
	url := c.cfg.BaseURL + "/ReadRealdata"
	payload := map[string]any{"datalist": tagNames}
	var result []VarValue

	err := c.retryWithBackoff(func() error {
		tok, e := c.getToken()
		if e != nil {
			return e
		}
		resp, e := c.doPOST(url, payload, map[string]string{"token": tok})
		if e != nil {
			return e
		}
		if isTokenInvalid(resp) {
			return fmt.Errorf("TOKEN_INVALID: %s", resp.Message)
		}
		if resp.Code != 0 {
			return fmt.Errorf("ReadRealdata 业务错误 code=%d: %s", resp.Code, resp.Message)
		}
		if e = json.Unmarshal(resp.Data, &result); e != nil {
			return fmt.Errorf("解析读取结果失败: %w", e)
		}
		return nil
	}, isTokenErr)

	return result, err
}

// ── WriteVariables（写入变量值）──────────────────────────────────────────────

// WriteVariables 批量写入变量值。
//
//	items: 键值对列表，如 []WriteItem{{"Tag1", 3.14}, {"Tag2", true}}
func (c *Client) WriteVariables(items []WriteItem) error {
	url := c.cfg.BaseURL + "/WriteVariables"
	payload := map[string]any{"datalist": items}

	return c.retryWithBackoff(func() error {
		tok, e := c.getToken()
		if e != nil {
			return e
		}
		resp, e := c.doPOST(url, payload, map[string]string{"token": tok})
		if e != nil {
			return e
		}
		if isTokenInvalid(resp) {
			return fmt.Errorf("TOKEN_INVALID: %s", resp.Message)
		}
		if resp.Code != 0 {
			return fmt.Errorf("WriteVariables 业务错误 code=%d: %s", resp.Code, resp.Message)
		}
		log.Printf("✅ 写入 %d 个变量成功", len(items))
		return nil
	}, isTokenErr)
}

// ── 内部辅助 ──────────────────────────────────────────────────────────────────

// isTokenErr 判断错误是否为 Token 失效（作为 retryWithBackoff 的 isCritical 回调）。
func isTokenErr(err error) bool {
	if err == nil {
		return false
	}
	return len(err.Error()) >= 13 && err.Error()[:13] == "TOKEN_INVALID"
}

// min8 返回 n 与 8 中的较小值，防止 Token 截取越界。
func min8(n int) int {
	if n < 8 {
		return n
	}
	return 8
}
