// Package menu 管理 IP编号+页码 的 LRU 字典，并自动同步到 SCADA 的 "menu" 变量。
//
// 特性：
//   - LRU 策略：最多 10 个菜单项，超出时淘汰最久未使用的
//   - 智能写入队列：并发写入时只保留最新值，上一条写完再写下一条
//   - 字符串长度上限 128 字符（SCADA 字符串类型限制）
//   - 线程安全：sync.Mutex 保护内存状态
package menu

import (
	"container/list"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── 常量 ─────────────────────────────────────────────────────────────────────

const (
	maxItems      = 10  // LRU 最大菜单项数
	maxStrLen     = 128 // SCADA 字符串上限（字节）
	menuVarName   = "menu"
	writeDebounce = 50 * time.Millisecond // 写入防抖延迟
)

// ── 数据结构 ──────────────────────────────────────────────────────────────────

// entry 是 LRU 链表节点存储的实际数据。
type entry struct {
	ipCode  string    // IP 标识（统一存为字符串）
	pageNum int       // 页码
	touchAt time.Time // 最后访问时间
}

// MenuItem 对外暴露的菜单项快照。
type MenuItem struct {
	IPCode      string  `json:"ip_code"`
	PageNum     int     `json:"page_num"`
	LastAccessed float64 `json:"last_accessed"` // Unix 时间戳，兼容 Python
}

// ScadaWriter 是 Manager 写入 SCADA 所依赖的接口，由 scada.Client 实现。
type ScadaWriter interface {
	WriteVariables(items []struct {
		N string `json:"N"`
		V string `json:"V"`
	}) error
}

// writeFunc 是实际写入 SCADA 的函数签名，避免循环导入。
type writeFunc func(n, v string) error

// Manager 是菜单管理器单例。
type Manager struct {
	mu      sync.Mutex
	lruList *list.List               // 双向链表，头=最久未使用
	lruMap  map[string]*list.Element // ipCode → 链表节点

	writeFn writeFunc // 注入的 SCADA 写入函数

	// 智能写入队列
	writing     bool   // 是否正在写入
	pendingVal  string // 最新待写入值（空字符串也是有效值，用 hasPending 区分）
	hasPending  bool
	writeMu     sync.Mutex
}

// ── 构造 ──────────────────────────────────────────────────────────────────────

// New 创建 Manager，writeFn 是向 SCADA 写单个变量的函数。
func New(writeFn writeFunc) *Manager {
	return &Manager{
		lruList: list.New(),
		lruMap:  make(map[string]*list.Element, maxItems+1),
		writeFn: writeFn,
	}
}

// ── 公共操作 ──────────────────────────────────────────────────────────────────

// AddOrUpdate 添加或更新菜单项，操作后自动同步到 SCADA。
func (m *Manager) AddOrUpdate(ipCode string, pageNum int) map[string]any {
	m.mu.Lock()

	action := "created"
	var evicted *MenuItem

	if el, ok := m.lruMap[ipCode]; ok {
		e := el.Value.(*entry)
		if e.pageNum == pageNum {
			// 相同页码，只刷新访问时间
			e.touchAt = time.Now()
			m.lruList.MoveToBack(el)
			action = "refreshed"
		} else {
			oldPage := e.pageNum
			e.pageNum = pageNum
			e.touchAt = time.Now()
			m.lruList.MoveToBack(el)
			action = "updated"
			log.Printf("🔄 更新菜单项: IP=%s, 旧页码=%d -> 新页码=%d", ipCode, oldPage, pageNum)
		}
	} else {
		// 新增前检查容量
		if m.lruList.Len() >= maxItems {
			evicted = m.evictLRU()
		}
		el := m.lruList.PushBack(&entry{ipCode: ipCode, pageNum: pageNum, touchAt: time.Now()})
		m.lruMap[ipCode] = el
		log.Printf("➕ 添加菜单项: IP=%s, 页码=%d (%d/%d)", ipCode, pageNum, m.lruList.Len(), maxItems)
	}

	menuStr := m.buildString()
	m.mu.Unlock()

	syncResult := m.syncToScada(menuStr)

	res := map[string]any{
		"code":          0,
		"message":       fmt.Sprintf("Menu item %s successfully", action),
		"action":        action,
		"menu_dict":     m.GetDict(),
		"menu_string":   menuStr,
		"current_count": m.Len(),
		"max_count":     maxItems,
		"sync_result":   syncResult,
	}
	if evicted != nil {
		res["evicted_item"] = evicted
		res["message"] = res["message"].(string) + fmt.Sprintf(" (evicted IP %s)", evicted.IPCode)
	}
	return res
}

// RemoveByIP 删除指定 IP 的菜单项。
func (m *Manager) RemoveByIP(ipCode string) map[string]any {
	m.mu.Lock()
	el, ok := m.lruMap[ipCode]
	if !ok {
		m.mu.Unlock()
		log.Printf("⚠️ 未找到 IP=%s 的菜单项", ipCode)
		return map[string]any{
			"code":      -1,
			"message":   fmt.Sprintf("No menu item found for IP %s", ipCode),
			"menu_dict": m.GetDict(),
		}
	}
	e := el.Value.(*entry)
	removedPage := e.pageNum
	m.lruList.Remove(el)
	delete(m.lruMap, ipCode)
	menuStr := m.buildString()
	m.mu.Unlock()

	log.Printf("🗑️ 删除菜单项: IP=%s, 页码=%d", ipCode, removedPage)
	syncResult := m.syncToScada(menuStr)

	return map[string]any{
		"code":          0,
		"message":       fmt.Sprintf("Removed menu item for IP %s", ipCode),
		"removed_item":  map[string]any{"ip_code": ipCode, "page_num": removedPage},
		"menu_dict":     m.GetDict(),
		"menu_string":   menuStr,
		"current_count": m.Len(),
		"sync_result":   syncResult,
	}
}

// RemoveExact 精确匹配 IP + 页码后删除。
func (m *Manager) RemoveExact(ipCode string, pageNum int) map[string]any {
	m.mu.Lock()
	el, ok := m.lruMap[ipCode]
	if !ok {
		m.mu.Unlock()
		return map[string]any{"code": -1, "message": fmt.Sprintf("No menu item found for IP %s", ipCode), "menu_dict": m.GetDict()}
	}
	e := el.Value.(*entry)
	if e.pageNum != pageNum {
		cur := e.pageNum
		m.mu.Unlock()
		return map[string]any{"code": -1, "message": fmt.Sprintf("Page mismatch: expected %d, got %d", pageNum, cur), "menu_dict": m.GetDict()}
	}
	m.lruList.Remove(el)
	delete(m.lruMap, ipCode)
	menuStr := m.buildString()
	m.mu.Unlock()

	log.Printf("🗑️ 精确删除: IP=%s, 页码=%d", ipCode, pageNum)
	syncResult := m.syncToScada(menuStr)

	return map[string]any{
		"code":          0,
		"message":       "Menu item removed successfully",
		"menu_dict":     m.GetDict(),
		"menu_string":   menuStr,
		"current_count": m.Len(),
		"sync_result":   syncResult,
	}
}

// ClearAll 清空所有菜单项并同步到 SCADA。
func (m *Manager) ClearAll() map[string]any {
	m.mu.Lock()
	cnt := m.lruList.Len()
	m.lruList.Init()
	m.lruMap = make(map[string]*list.Element, maxItems+1)
	m.mu.Unlock()

	log.Printf("🧹 清空所有菜单项 (共 %d 项)", cnt)
	syncResult := m.syncToScada("")

	return map[string]any{
		"code":          0,
		"message":       fmt.Sprintf("Cleared %d menu items", cnt),
		"menu_dict":     map[string]int{},
		"menu_list":     []any{},
		"menu_string":   "",
		"current_count": 0,
		"sync_result":   syncResult,
	}
}

// SetDict 整体替换菜单字典（超出 maxItems 的部分截断）。
func (m *Manager) SetDict(dict map[string]int) map[string]any {
	m.mu.Lock()
	m.lruList.Init()
	m.lruMap = make(map[string]*list.Element, maxItems+1)

	// 保持稳定顺序（按 key 排序后截取前 maxItems）
	keys := make([]string, 0, len(dict))
	for k := range dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > maxItems {
		keys = keys[:maxItems]
	}

	now := time.Now()
	for _, k := range keys {
		el := m.lruList.PushBack(&entry{ipCode: k, pageNum: dict[k], touchAt: now})
		m.lruMap[k] = el
	}
	truncated := len(dict) > maxItems
	menuStr := m.buildString()
	m.mu.Unlock()

	log.Printf("📝 设置菜单字典: %d 项", m.Len())
	syncResult := m.syncToScada(menuStr)

	res := map[string]any{
		"code":          0,
		"message":       fmt.Sprintf("Menu dict set with %d items", m.Len()),
		"menu_dict":     m.GetDict(),
		"menu_list":     m.GetList(),
		"menu_string":   menuStr,
		"current_count": m.Len(),
		"max_count":     maxItems,
		"sync_result":   syncResult,
	}
	if truncated {
		res["warning"] = fmt.Sprintf("Truncated %d items (max %d)", len(dict)-maxItems, maxItems)
	}
	return res
}

// GetDict 返回当前菜单字典快照 {ipCode: pageNum}。
func (m *Manager) GetDict() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, m.lruList.Len())
	for el := m.lruList.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		out[e.ipCode] = e.pageNum
	}
	return out
}

// GetList 返回带时间戳的菜单列表（LRU 顺序，最旧在前）。
func (m *Manager) GetList() []MenuItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MenuItem, 0, m.lruList.Len())
	for el := m.lruList.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		out = append(out, MenuItem{
			IPCode:       e.ipCode,
			PageNum:      e.pageNum,
			LastAccessed: float64(e.touchAt.UnixNano()) / 1e9,
		})
	}
	return out
}

// GetString 返回当前 SCADA 菜单字符串。
func (m *Manager) GetString() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buildString()
}

// Len 返回当前菜单项数量（线程安全）。
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lruList.Len()
}

// ── 内部方法 ──────────────────────────────────────────────────────────────────

// buildString 生成菜单字符串（调用方须持有 mu 锁）。
// 格式：IP_页码@IP_页码，按 IP 排序（数字优先，再字母）。
func (m *Manager) buildString() string {
	if m.lruList.Len() == 0 {
		return ""
	}
	type kv struct{ k string; v int }
	items := make([]kv, 0, m.lruList.Len())
	for el := m.lruList.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		items = append(items, kv{e.ipCode, e.pageNum})
	}
	// 排序：纯数字串优先，其余按字典序
	sort.Slice(items, func(i, j int) bool {
		ni, oki := isNumeric(items[i].k)
		nj, okj := isNumeric(items[j].k)
		if oki && okj {
			return ni < nj
		}
		if oki {
			return true
		}
		if okj {
			return false
		}
		return items[i].k < items[j].k
	})

	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = fmt.Sprintf("%s_%d", it.k, it.v)
	}
	s := strings.Join(parts, "@")
	if len(s) > maxStrLen {
		log.Printf("⚠️ 菜单字符串超过 %d 字符: %d 字符", maxStrLen, len(s))
	}
	return s
}

// evictLRU 淘汰链表头部（最久未使用），调用方须持有 mu 锁。
func (m *Manager) evictLRU() *MenuItem {
	front := m.lruList.Front()
	if front == nil {
		return nil
	}
	e := front.Value.(*entry)
	m.lruList.Remove(front)
	delete(m.lruMap, e.ipCode)
	log.Printf("🗑️ LRU 淘汰: IP=%s, 页码=%d", e.ipCode, e.pageNum)
	return &MenuItem{IPCode: e.ipCode, PageNum: e.pageNum}
}

// syncToScada 使用智能写入队列把 menuStr 写入 SCADA。
// 正在写入时：只保留最新值，等当前写完后再写一次。
func (m *Manager) syncToScada(menuStr string) map[string]any {
	m.writeMu.Lock()

	if m.writing {
		// 有进行中的写入，将新值排队（抛弃旧的排队值）
		if m.hasPending {
			log.Printf("⚠️ 抛弃旧排队值，使用最新值")
		}
		m.pendingVal = menuStr
		m.hasPending = true
		m.writeMu.Unlock()
		return map[string]any{
			"code":    0,
			"message": "queued",
			"queued":  true,
		}
	}

	// 当前无写入，立即写
	m.writing = true
	m.writeMu.Unlock()

	err := m.doWrite(menuStr)

	// 循环处理排队值
	for {
		m.writeMu.Lock()
		if !m.hasPending {
			m.writing = false
			m.writeMu.Unlock()
			break
		}
		next := m.pendingVal
		m.hasPending = false
		m.writeMu.Unlock()

		time.Sleep(writeDebounce)
		_ = m.doWrite(next)
	}

	if err != nil {
		return map[string]any{"code": -1, "message": err.Error()}
	}
	return map[string]any{"code": 0, "message": "success"}
}

// doWrite 执行一次实际的 SCADA 写入。
func (m *Manager) doWrite(menuStr string) error {
	if err := m.writeFn(menuVarName, menuStr); err != nil {
		log.Printf("❌ 菜单写入 SCADA 失败: %v", err)
		return err
	}
	log.Printf("📤 菜单已同步到 SCADA: %s", truncate(menuStr, 60))
	return nil
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

// isNumeric 判断字符串是否为纯数字，返回数值和判断结果。
func isNumeric(s string) (int64, bool) {
	if len(s) == 0 {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
