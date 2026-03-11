// server.go 将 MenuManager 的能力暴露为 HTTP JSON 接口。
// 对应 Python menu_router.py。
//
// 路由一览：
//   POST   /api/menu/add                添加或更新菜单项
//   POST   /api/menu/remove-by-ip       按 IP 删除
//   POST   /api/menu/remove-exact       精确匹配删除
//   GET    /api/menu/list               获取菜单列表
//   POST   /api/menu/clear              清空所有
//   POST   /api/menu/set                整体替换
//   GET    /api/menu/add/{ip}/{page}    简化 GET 版本
//   GET    /api/menu/remove-by-ip/{ip}  简化 GET 版本
//   GET    /api/menu/unregister/{name}  注销用户（GET，兼容 SCADA 上位机）
//   DELETE /api/menu/unregister/{name}  注销用户（DELETE）

package menu

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// RegisterRoutes 将所有菜单路由挂载到 mux 上。
// prefix 通常为 "/api/menu"。
func (m *Manager) RegisterRoutes(mux *http.ServeMux, prefix string) {
	// 精确路径（固定路由）
	mux.HandleFunc(prefix+"/add", m.handleAdd)
	mux.HandleFunc(prefix+"/remove-by-ip", m.handleRemoveByIP)
	mux.HandleFunc(prefix+"/remove-exact", m.handleRemoveExact)
	mux.HandleFunc(prefix+"/list", m.handleList)
	mux.HandleFunc(prefix+"/clear", m.handleClear)
	mux.HandleFunc(prefix+"/set", m.handleSet)

	// 带路径参数的路由（用前缀匹配，内部手动解析）
	mux.HandleFunc(prefix+"/add/", m.handleAddGet)
	mux.HandleFunc(prefix+"/remove-by-ip/", m.handleRemoveByIPGet)
	mux.HandleFunc(prefix+"/unregister/", m.handleUnregister)
}

// ── 响应辅助 ──────────────────────────────────────────────────────────────────

func menuReply(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	_ = json.NewEncoder(w).Encode(payload)
}

func menuFail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": -1, "message": msg})
}

// ── POST /api/menu/add ────────────────────────────────────────────────────────

func (m *Manager) handleAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodPost {
		menuFail(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var body struct {
		IPCode  string `json:"ip_code"`
		PageNum int    `json:"page_num"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPCode == "" {
		menuFail(w, http.StatusBadRequest, `格式错误，需要 {"ip_code":"设备A","page_num":78}`)
		return
	}
	menuReply(w, m.AddOrUpdate(body.IPCode, body.PageNum))
}

// ── POST /api/menu/remove-by-ip ───────────────────────────────────────────────

func (m *Manager) handleRemoveByIP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodPost {
		menuFail(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var body struct {
		IPCode string `json:"ip_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPCode == "" {
		menuFail(w, http.StatusBadRequest, `格式错误，需要 {"ip_code":"设备A"}`)
		return
	}
	menuReply(w, m.RemoveByIP(body.IPCode))
}

// ── POST /api/menu/remove-exact ───────────────────────────────────────────────

func (m *Manager) handleRemoveExact(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodPost {
		menuFail(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var body struct {
		IPCode  string `json:"ip_code"`
		PageNum int    `json:"page_num"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IPCode == "" {
		menuFail(w, http.StatusBadRequest, `格式错误，需要 {"ip_code":"设备A","page_num":78}`)
		return
	}
	menuReply(w, m.RemoveExact(body.IPCode, body.PageNum))
}

// ── GET /api/menu/list ────────────────────────────────────────────────────────

func (m *Manager) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	menuStr := m.GetString()
	menuReply(w, map[string]any{
		"code":          0,
		"message":       "Menu list retrieved successfully",
		"menu_dict":     m.GetDict(),
		"menu_list":     m.GetList(),
		"menu_string":   menuStr,
		"current_count": m.Len(),
		"max_count":     maxItems,
		"string_length": utf8.RuneCountInString(menuStr),
		"string_limit":  maxStrLen,
	})
}

// ── POST /api/menu/clear ──────────────────────────────────────────────────────

func (m *Manager) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodPost {
		menuFail(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	menuReply(w, m.ClearAll())
}

// ── POST /api/menu/set ────────────────────────────────────────────────────────

func (m *Manager) handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodPost {
		menuFail(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var body struct {
		MenuItems []struct {
			IPCode  string `json:"ip_code"`
			PageNum int    `json:"page_num"`
		} `json:"menu_items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		menuFail(w, http.StatusBadRequest, `格式错误，需要 {"menu_items":[{"ip_code":"设备A","page_num":78}]}`)
		return
	}
	dict := make(map[string]int, len(body.MenuItems))
	for _, it := range body.MenuItems {
		if it.IPCode != "" {
			dict[it.IPCode] = it.PageNum
		}
	}
	menuReply(w, m.SetDict(dict))
}

// ── GET /api/menu/add/{ip}/{page} ─────────────────────────────────────────────

func (m *Manager) handleAddGet(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	// 路径格式：/api/menu/add/{ip}/{page}
	// prefix = "/api/menu/add/"  → 剩余部分为 "{ip}/{page}"
	parts := splitTail(r.URL.Path, "/api/menu/add/")
	if len(parts) < 2 {
		menuFail(w, http.StatusBadRequest, "路径格式: /api/menu/add/{ip_code}/{page_num}")
		return
	}
	ipCode := parts[0]
	pageNum, err := strconv.Atoi(parts[1])
	if err != nil {
		menuFail(w, http.StatusBadRequest, "page_num 必须为整数")
		return
	}
	menuReply(w, m.AddOrUpdate(ipCode, pageNum))
}

// ── GET /api/menu/remove-by-ip/{ip} ──────────────────────────────────────────

func (m *Manager) handleRemoveByIPGet(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	parts := splitTail(r.URL.Path, "/api/menu/remove-by-ip/")
	if len(parts) < 1 || parts[0] == "" {
		menuFail(w, http.StatusBadRequest, "路径格式: /api/menu/remove-by-ip/{ip_code}")
		return
	}
	menuReply(w, m.RemoveByIP(parts[0]))
}

// ── GET|DELETE /api/menu/unregister/{name} ────────────────────────────────────
// 兼容 SCADA 上位机 RequestJsonInfo（GET）和 RESTful 规范（DELETE）

func (m *Manager) handleUnregister(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsOK(w)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		menuFail(w, http.StatusMethodNotAllowed, "仅支持 GET / DELETE")
		return
	}

	parts := splitTail(r.URL.Path, "/api/menu/unregister/")
	if len(parts) < 1 || parts[0] == "" {
		menuFail(w, http.StatusBadRequest, "路径格式: /api/menu/unregister/{name}")
		return
	}
	name := parts[0]

	// 合法性校验（与 Python 版本一致）
	if err := validateName(name); err != nil {
		menuFail(w, http.StatusBadRequest, err.Error())
		return
	}

	res := m.RemoveByIP(name)
	if res["code"] == -1 {
		menuFail(w, http.StatusNotFound, res["message"].(string))
		return
	}

	menuReply(w, map[string]any{
		"code":              0,
		"message":           "User '" + name + "' has been successfully unregistered",
		"unregistered_user": name,
		"removed_item":      res["removed_item"],
		"menu_dict":         res["menu_dict"],
		"menu_string":       res["menu_string"],
		"current_count":     res["current_count"],
		"sync_result":       res["sync_result"],
		"timestamp":         float64(time.Now().UnixNano()) / 1e9,
	})
}

// ── 工具函数 ──────────────────────────────────────────────────────────────────

// corsOK 返回 CORS 预检响应。
func corsOK(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.WriteHeader(http.StatusNoContent)
}

// splitTail 去掉路径中的 prefix 后，按 "/" 拆分剩余部分。
func splitTail(path, prefix string) []string {
	tail := strings.TrimPrefix(path, prefix)
	tail = strings.Trim(tail, "/")
	if tail == "" {
		return nil
	}
	return strings.SplitN(tail, "/", 2)
}

// validateName 验证 unregister 的 name 合法性。
var dangerousChars = []string{"@", "/", "\\", "\n", "\r", "\t", "\x00", ";", "|", "&", "$", "`", "<", ">", `"`, "'"}

func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errorf("名称不能为空")
	}
	if utf8.RuneCountInString(name) > 100 {
		return errorf("名称过长（最多 100 字符）")
	}
	for _, ch := range dangerousChars {
		if strings.Contains(name, ch) {
			return errorf("名称包含非法字符: " + ch)
		}
	}
	return nil
}

func errorf(msg string) error {
	return &menuErr{msg}
}

type menuErr struct{ msg string }

func (e *menuErr) Error() string { return e.msg }
