// Package logger 拦截标准 log 输出，解析级别与来源，
// 维护内存环形缓冲 + 48 小时 JSONL 持久化文件，
// 并通过 Wails runtime 事件实时推送到前端。
package logger

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ── 数据结构 ──────────────────────────────────────────────────────────────────

// Level 日志级别
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Entry 单条日志记录
type Entry struct {
	Time    string `json:"time"`    // "15:04:05.000"
	Level   Level  `json:"level"`   // info / warn / error
	Source  string `json:"source"`  // 来源标签，如 "scada" "menu" "app"
	Message string `json:"message"` // 正文
}

// ── 常量 ─────────────────────────────────────────────────────────────────────

const (
	maxMemEntries = 2000            // 内存最多保留条数
	keepDuration  = 48 * time.Hour // 持久化保留时长
	eventName     = "log:entry"    // Wails 前端事件名
)

// ── Hub ───────────────────────────────────────────────────────────────────────

// Hub 是日志中枢，全局单例。
type Hub struct {
	mu      sync.RWMutex
	entries []Entry // 环形内存缓冲

	ctx     context.Context // Wails runtime ctx
	logFile *os.File        // 当天的持久化文件
	logDir  string          // 日志目录（exe 同级）
}

var hub *Hub

// Init 必须在 Wails startup 回调中调用，传入 Wails ctx 和日志目录。
func Init(ctx context.Context, logDir string) {
	h := &Hub{
		ctx:    ctx,
		logDir: logDir,
	}
	hub = h

	// 打开/创建今天的日志文件
	h.openLogFile()

	// 加载过去 48 小时的历史记录到内存
	h.loadHistory()

	// 接管标准 log 输出
	log.SetOutput(&logWriter{hub: h})
	log.SetFlags(0) // 我们自己加时间戳，关掉默认前缀

	// 启动每天午夜滚动日志文件的协程
	go h.rotateDaemon(ctx)
}

// GetHistory 返回内存中所有历史条目（供前端首次连接时拉取）。
func GetHistory() []Entry {
	if hub == nil {
		return nil
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	cp := make([]Entry, len(hub.entries))
	copy(cp, hub.entries)
	return cp
}

// ── logWriter：接管 log.SetOutput ────────────────────────────────────────────

// logWriter 实现 io.Writer，接管标准库 log 的所有输出。
type logWriter struct{ hub *Hub }

func (w *logWriter) Write(p []byte) (int, error) {
	w.hub.ingest(string(p))
	return len(p), nil
}

// ── ingest：解析并分发一条日志行 ─────────────────────────────────────────────

// ingest 解析原始日志字符串，生成 Entry，写内存、文件、推送前端。
func (h *Hub) ingest(raw string) {
	raw = strings.TrimRight(raw, "\n\r")
	if raw == "" {
		return
	}

	e := Entry{
		Time:    time.Now().Format("15:04:05.000"),
		Level:   parseLevel(raw),
		Source:  parseSource(raw),
		Message: raw,
	}

	// 写内存
	h.mu.Lock()
	h.entries = append(h.entries, e)
	if len(h.entries) > maxMemEntries {
		h.entries = h.entries[len(h.entries)-maxMemEntries:]
	}
	h.mu.Unlock()

	// 写文件
	h.writeFile(e)

	// 推送 Wails 事件到前端（ctx 已初始化才推）
	if h.ctx != nil {
		runtime.EventsEmit(h.ctx, eventName, e)
	}
}

// ── 级别与来源解析 ────────────────────────────────────────────────────────────

func parseLevel(s string) Level {
	sl := strings.ToLower(s)
	switch {
	case strings.Contains(sl, "❌") || strings.Contains(sl, "error") || strings.Contains(sl, "err "):
		return LevelError
	case strings.Contains(sl, "⚠️") || strings.Contains(sl, "warn") || strings.Contains(sl, "warning"):
		return LevelWarn
	default:
		return LevelInfo
	}
}

// sourceKeywords 按优先级匹配来源标签
var sourceKeywords = []struct {
	tag      string
	keywords []string
}{
	{"db",    []string{"[db]", "mysql", "连接池"}},
	{"scada", []string{"scada", "token", "gettoken", "deltoken", "realtdata", "getvariable", "writevariable"}},
	{"menu",  []string{"menu", "lru", "菜单"}},
	{"http",  []string{"http", "/api/", "listen"}},
	{"app",   []string{"startup", "shutdown", "config", "配置"}},
}

func parseSource(s string) string {
	sl := strings.ToLower(s)
	for _, sk := range sourceKeywords {
		for _, kw := range sk.keywords {
			if strings.Contains(sl, kw) {
				return sk.tag
			}
		}
	}
	return "app"
}

// ── 持久化 ────────────────────────────────────────────────────────────────────

func (h *Hub) logFileName(t time.Time) string {
	return filepath.Join(h.logDir, fmt.Sprintf("goks-%s.jsonl", t.Format("2006-01-02")))
}

func (h *Hub) openLogFile() {
	name := h.logFileName(time.Now())
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ 无法打开日志文件: %v\n", err)
		return
	}
	h.logFile = f
}

func (h *Hub) writeFile(e Entry) {
	if h.logFile == nil {
		return
	}
	line, _ := json.Marshal(e)
	h.mu.Lock()
	_, _ = h.logFile.Write(append(line, '\n'))
	h.mu.Unlock()
}

// loadHistory 读取过去 48h 内的 JSONL 文件，加载到内存。
func (h *Hub) loadHistory() {
	cutoff := time.Now().Add(-keepDuration)
	for d := 0; d <= 2; d++ {
		day := time.Now().AddDate(0, 0, -d)
		if day.Before(cutoff) {
			break
		}
		name := h.logFileName(day)
		h.loadFile(name, cutoff)
	}
}

func (h *Hub) loadFile(name string, cutoff time.Time) {
	f, err := os.Open(name)
	if err != nil {
		return
	}
	defer f.Close()

	today := time.Now().Format("2006-01-02")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		// 用文件日期 + 条目时间重建完整时间
		full := today + " " + e.Time
		t, err := time.Parse("2006-01-02 15:04:05.000", full)
		if err != nil || t.Before(cutoff) {
			continue
		}
		h.entries = append(h.entries, e)
	}
}

// rotateDaemon 每天午夜滚动到新文件，同时删除超过 48h 的旧文件。
func (h *Hub) rotateDaemon(ctx context.Context) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 1, 0, now.Location())
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			h.mu.Lock()
			if h.logFile != nil {
				_ = h.logFile.Close()
			}
			h.openLogFile()
			h.mu.Unlock()
			// 删除 48h 前的旧文件
			h.cleanOldFiles()
		}
	}
}

func (h *Hub) cleanOldFiles() {
	cutoff := time.Now().Add(-keepDuration)
	entries, _ := os.ReadDir(h.logDir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "goks-") || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "goks-"), ".jsonl")
		t, err := time.Parse("2006-01-02", dateStr)
		if err == nil && t.Before(cutoff) {
			_ = os.Remove(filepath.Join(h.logDir, e.Name()))
		}
	}
}
