// db/batch.go 批量 SQL 执行器：接收大量 INSERT/UPDATE 语句，
// 以 500ms/50条 为一批异步执行，配合三级队列保障数据不丢失。
//
// 架构：实时队列(chan) → 批量事务执行 → 重试队列(指数退避) → 死信队列(内存+文件)
// 持久化：WAL 预写日志确保异常崩溃不丢数据，优雅关闭时排空队列。
package db

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── 数据结构 ─────────────────────────────────────────────────────────────────

// SQLItem 单条待执行 SQL
type SQLItem struct {
	SeqNo        int64  `json:"seq_no"`
	SQL          string `json:"sql"`
	Conn         string `json:"conn"`
	EnqueuedAt   string `json:"enqueued_at"`
	Retries      int    `json:"retries"`
	ConnRetries  int    `json:"conn_retries,omitempty"`
	RetryKind    string `json:"retry_kind,omitempty"`
	FirstRetryAt string `json:"first_retry_at,omitempty"`
	NextRetryAt  string `json:"next_retry_at,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	nextRetry    time.Time
}

type retryStateFile struct {
	UpdatedAt  string     `json:"updated_at"`
	RetryQ     []*SQLItem `json:"retry_q,omitempty"`
	ConnRetryQ []*SQLItem `json:"conn_retry_q,omitempty"`
}

// DeadItem 死信条目
type DeadItem struct {
	SQL        string `json:"sql"`
	Conn       string `json:"conn"`
	Error      string `json:"error"`
	Retries    int    `json:"retries"`
	EnqueuedAt string `json:"enqueued_at"`
	FailedAt   string `json:"failed_at"`
}

// BatchStats 单连接队列统计
type BatchStats struct {
	Conn           string `json:"conn"`
	QueueSize      int    `json:"queue_size"`
	QueueCapacity  int    `json:"queue_capacity"`
	RetrySize      int    `json:"retry_size"`
	ConnRetrySize  int    `json:"conn_retry_size"`
	DeadLetterSize int64  `json:"dead_letter_size"`
	TotalProcessed int64  `json:"total_processed"`
	TotalFailed    int64  `json:"total_failed"`
	LastFlush      string `json:"last_flush"`
	OldestRetryAt  string `json:"oldest_retry_at"`
	LastRetryError string `json:"last_retry_error"`
	RetryFileSize  int64  `json:"retry_file_size"`
}

// ── NOW() 替换 ───────────────────────────────────────────────────────────────

// nowRegex 匹配 SQL 中的 NOW()，大小写不敏感
var nowRegex = regexp.MustCompile(`(?i)\bNOW\s*\(\s*\)`)

const (
	retryKindNormal = "retry"
	retryKindConn   = "conn_retry"
	timeLayout      = "2006-01-02 15:04:05"
)

// ── 瞬态错误判断 ─────────────────────────────────────────────────────────────

// isDisconnectError 判断是否为数据库断连类错误。
// 这类错误进入长期退避重试，不直接进入死信。
func isDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return err == ErrNotConnected ||
		strings.Contains(msg, "dial") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "reset by peer") ||
		strings.Contains(msg, "invalid connection") ||
		strings.Contains(msg, "bad connection") ||
		strings.Contains(msg, "server has gone away") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "database is closed")
}

// isTransientError 判断是否为普通瞬态错误（有限次数重试）。
// 语法错误、约束冲突等确定性失败不重试，直接进死信。
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if isDisconnectError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "too many connections") ||
		strings.Contains(msg, "deadlock")
}

func parseStoredTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation(timeLayout, s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func setItemNextRetry(item *SQLItem, t time.Time) {
	item.nextRetry = t
	item.NextRetryAt = t.Format(timeLayout)
}

func markRetryStart(item *SQLItem) {
	if item.FirstRetryAt == "" {
		item.FirstRetryAt = time.Now().Format(timeLayout)
	}
}

func cloneSQLItem(item *SQLItem) *SQLItem {
	if item == nil {
		return nil
	}
	cp := *item
	cp.nextRetry = time.Time{}
	return &cp
}

// ── connBatch: 单连接批量执行上下文 ──────────────────────────────────────────

// I/O 缓冲区大小：机械硬盘场景下用大缓冲减少磁盘寻道和零碎写入
const ioBufSize = 64 * 1024 // 64KB

// connBatch 管理单个数据库连接的批量执行队列。
// 每个连接拥有独立的实时队列、重试队列、死信队列、WAL 文件和刷出协程。
type connBatch struct {
	name    string
	manager *Manager
	cfg     BatchConfig

	// 实时队列（阻塞式 channel，满时入队阻塞等待）
	queue chan *SQLItem

	// 重试队列（指数退避）
	retryMu sync.Mutex
	retryQ  []*SQLItem

	// 断连类错误长期重试队列（指数退避，无固定上限）
	connRetryMu sync.Mutex
	connRetryQ  []*SQLItem

	retryStateDirty atomic.Bool

	// 死信 - 内存保留最近 N 条，同时写 JSONL 文件
	deadMu       sync.Mutex
	deadQ        []DeadItem
	deadCount    int64
	deadFile     *os.File      // 死信文件常驻句柄（减少反复 Open/Close）
	deadWriter   *bufio.Writer // 死信文件缓冲写入
	deadFileDate string        // 当前死信文件的日期，日期变化时滚动

	// WAL 预写日志（基于 done-set 的精确追踪方案）
	walMu     sync.Mutex
	walFile   *os.File
	walWriter *bufio.Writer
	walSeq    int64 // 递增序号

	// 统计
	totalProcessed atomic.Int64
	totalFailed    atomic.Int64
	lastFlush      atomic.Value // string

	dataDir string
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// newConnBatch 创建并启动单连接批量执行上下文
func newConnBatch(name string, mgr *Manager, cfg BatchConfig, dataDir string) *connBatch {
	ctx, cancel := context.WithCancel(context.Background())
	cb := &connBatch{
		name:    name,
		manager: mgr,
		cfg:     cfg,
		queue:   make(chan *SQLItem, cfg.QueueSize),
		dataDir: dataDir,
		ctx:     ctx,
		cancel:  cancel,
	}
	cb.lastFlush.Store("-")
	cb.openWAL()
	cb.openDeadFile()
	doneSeqs := cb.loadWALDoneSet()
	retrySeqs := cb.recoverRetryState(doneSeqs)
	cb.recoverWAL(retrySeqs)

	cb.wg.Add(2)
	go cb.flushLoop()
	go cb.ioFlushLoop()

	return cb
}

// ── WAL 文件操作 ─────────────────────────────────────────────────────────────

func (cb *connBatch) walPath() string {
	return filepath.Join(cb.dataDir, fmt.Sprintf("batch-wal-%s.jsonl", cb.name))
}

func (cb *connBatch) retryStatePath() string {
	return filepath.Join(cb.dataDir, fmt.Sprintf("batch-retry-%s.json", cb.name))
}

func (cb *connBatch) loadWALDoneSet() map[int64]bool {
	data, err := os.ReadFile(cb.walPath())
	if err != nil || len(data) == 0 {
		return nil
	}
	doneSet := make(map[int64]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var donePeek struct {
			Done []int64 `json:"done"`
		}
		if json.Unmarshal(line, &donePeek) == nil && len(donePeek.Done) > 0 {
			for _, seq := range donePeek.Done {
				doneSet[seq] = true
			}
		}
	}
	return doneSet
}

func (cb *connBatch) retryStateFileSize() int64 {
	info, err := os.Stat(cb.retryStatePath())
	if err != nil {
		return 0
	}
	return info.Size()
}

func (cb *connBatch) markRetryStateDirty() {
	cb.retryStateDirty.Store(true)
}

func (cb *connBatch) recoverRetryState(doneSeqs map[int64]bool) map[int64]bool {
	_ = os.Remove(cb.retryStatePath() + ".tmp")
	data, err := os.ReadFile(cb.retryStatePath())
	if err != nil || len(data) == 0 {
		return nil
	}

	var state retryStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[batch:%s] ⚠️ 重试状态文件解析失败: %v", cb.name, err)
		return nil
	}

	seqSet := make(map[int64]bool)
	recoveredRetry := 0
	recoveredConnRetry := 0

	cb.retryMu.Lock()
	for _, item := range state.RetryQ {
		if item == nil || item.SeqNo <= 0 || (doneSeqs != nil && doneSeqs[item.SeqNo]) {
			continue
		}
		item.RetryKind = retryKindNormal
		item.nextRetry = parseStoredTime(item.NextRetryAt)
		if item.nextRetry.IsZero() {
			item.nextRetry = time.Now()
		}
		cb.retryQ = append(cb.retryQ, item)
		seqSet[item.SeqNo] = true
		recoveredRetry++
	}
	cb.retryMu.Unlock()

	cb.connRetryMu.Lock()
	for _, item := range state.ConnRetryQ {
		if item == nil || item.SeqNo <= 0 || (doneSeqs != nil && doneSeqs[item.SeqNo]) {
			continue
		}
		item.RetryKind = retryKindConn
		item.nextRetry = parseStoredTime(item.NextRetryAt)
		if item.nextRetry.IsZero() {
			item.nextRetry = time.Now()
		}
		cb.connRetryQ = append(cb.connRetryQ, item)
		seqSet[item.SeqNo] = true
		recoveredConnRetry++
	}
	cb.connRetryMu.Unlock()

	if recoveredRetry > 0 || recoveredConnRetry > 0 {
		log.Printf("[batch:%s] ✅ 从重试状态文件恢复了 %d 条普通重试、%d 条长期重试", cb.name, recoveredRetry, recoveredConnRetry)
	}
	return seqSet
}

func (cb *connBatch) persistRetryState() {
	if !cb.retryStateDirty.Load() {
		return
	}

	cb.retryMu.Lock()
	retryQ := make([]*SQLItem, 0, len(cb.retryQ))
	for _, item := range cb.retryQ {
		if item == nil {
			continue
		}
		cp := cloneSQLItem(item)
		if cp.NextRetryAt == "" && !item.nextRetry.IsZero() {
			cp.NextRetryAt = item.nextRetry.Format(timeLayout)
		}
		retryQ = append(retryQ, cp)
	}
	cb.retryMu.Unlock()

	cb.connRetryMu.Lock()
	connRetryQ := make([]*SQLItem, 0, len(cb.connRetryQ))
	for _, item := range cb.connRetryQ {
		if item == nil {
			continue
		}
		cp := cloneSQLItem(item)
		if cp.NextRetryAt == "" && !item.nextRetry.IsZero() {
			cp.NextRetryAt = item.nextRetry.Format(timeLayout)
		}
		connRetryQ = append(connRetryQ, cp)
	}
	cb.connRetryMu.Unlock()

	path := cb.retryStatePath()
	if len(retryQ) == 0 && len(connRetryQ) == 0 {
		_ = os.Remove(path)
		cb.retryStateDirty.Store(false)
		return
	}

	state := retryStateFile{
		UpdatedAt:  time.Now().Format(timeLayout),
		RetryQ:     retryQ,
		ConnRetryQ: connRetryQ,
	}
	data, err := json.Marshal(state)
	if err != nil {
		log.Printf("[batch:%s] ⚠️ 序列化重试状态失败: %v", cb.name, err)
		return
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		log.Printf("[batch:%s] ⚠️ 写入重试状态临时文件失败: %v", cb.name, err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		log.Printf("[batch:%s] ⚠️ 替换重试状态文件失败: %v", cb.name, err)
		return
	}
	cb.retryStateDirty.Store(false)
}

func (cb *connBatch) openWAL() {
	cb.walMu.Lock()
	defer cb.walMu.Unlock()

	f, err := os.OpenFile(cb.walPath(), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[batch:%s] ⚠️ 打开 WAL 文件失败: %v", cb.name, err)
		return
	}
	cb.walFile = f
	cb.walWriter = bufio.NewWriterSize(f, ioBufSize)
}

// openDeadFile 打开死信文件常驻句柄（避免每次写入都 Open/Close 消耗磁盘寻道）
func (cb *connBatch) openDeadFile() {
	cb.deadMu.Lock()
	defer cb.deadMu.Unlock()
	cb.openDeadFileLocked()
}

func (cb *connBatch) openDeadFileLocked() {
	today := time.Now().Format("2006-01-02")
	if cb.deadFile != nil && cb.deadFileDate == today {
		return // 同一天，无需重新打开
	}
	// 关闭旧句柄
	if cb.deadWriter != nil {
		cb.deadWriter.Flush()
	}
	if cb.deadFile != nil {
		cb.deadFile.Close()
	}
	path := filepath.Join(cb.dataDir, fmt.Sprintf("batch-dead-%s-%s.jsonl", cb.name, today))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[batch:%s] ⚠️ 打开死信文件失败: %v", cb.name, err)
		cb.deadFile = nil
		cb.deadWriter = nil
		return
	}
	cb.deadFile = f
	cb.deadWriter = bufio.NewWriterSize(f, ioBufSize)
	cb.deadFileDate = today
}

// walAppend 将 SQL 条目追加到 WAL 缓冲（不立即 flush，批量入队后统一 flush）
func (cb *connBatch) walAppend(item *SQLItem) {
	cb.walMu.Lock()
	defer cb.walMu.Unlock()

	if cb.walWriter == nil {
		return
	}
	cb.walSeq++
	item.SeqNo = cb.walSeq
	data, _ := json.Marshal(item)
	cb.walWriter.Write(data)
	cb.walWriter.WriteByte('\n')
}

// walFlush 刷出 WAL 缓冲到磁盘
func (cb *connBatch) walFlush() {
	cb.walMu.Lock()
	defer cb.walMu.Unlock()
	if cb.walWriter != nil {
		cb.walWriter.Flush()
	}
}

// walMarkDone 标记一组条目为已完成（成功执行或进入死信）。
// WAL 采用 done-set 方案：每个批次的已完成序号独立记录，恢复时精确跳过。
// 注意：仅写入缓冲，不立即 Flush（由 ioFlushLoop 定时刷盘，减少磁盘 I/O）。
func (cb *connBatch) walMarkDone(seqs []int64) {
	var valid []int64
	for _, s := range seqs {
		if s > 0 {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		return
	}

	cb.walMu.Lock()
	defer cb.walMu.Unlock()
	if cb.walWriter == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{"done": valid})
	cb.walWriter.Write(data)
	cb.walWriter.WriteByte('\n')
	// 不 Flush — 由 ioFlushLoop 每 2 秒统一刷盘
}

// walCompact 压缩 WAL 文件：收集所有 done 集合，仅保留未完成的条目
func (cb *connBatch) walCompact() {
	cb.walMu.Lock()
	defer cb.walMu.Unlock()

	if cb.walFile == nil {
		return
	}
	_ = cb.walWriter.Flush()
	_ = cb.walFile.Close()

	walPath := cb.walPath()
	data, err := os.ReadFile(walPath)
	if err != nil {
		log.Printf("[batch:%s] WAL 压缩读取失败: %v", cb.name, err)
		cb.reopenWALLocked()
		return
	}

	if len(data) == 0 {
		cb.reopenWALLocked()
		return
	}

	// 第一遍：收集所有已完成的序号
	doneSet := make(map[int64]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var donePeek struct {
			Done []int64 `json:"done"`
		}
		if json.Unmarshal(line, &donePeek) == nil && len(donePeek.Done) > 0 {
			for _, seq := range donePeek.Done {
				doneSet[seq] = true
			}
		}
	}

	// 第二遍：保留未完成的条目
	var remaining [][]byte
	scanner = bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var peek struct {
			SeqNo int64   `json:"seq_no"`
			Done  []int64 `json:"done"`
		}
		if json.Unmarshal(line, &peek) != nil {
			continue
		}
		// 跳过 done 标记行
		if len(peek.Done) > 0 {
			continue
		}
		// 保留未完成的 SQL 条目
		if peek.SeqNo > 0 && !doneSet[peek.SeqNo] {
			cp := make([]byte, len(line))
			copy(cp, line)
			remaining = append(remaining, cp)
		}
	}

	// 重写 WAL 文件
	f, err := os.Create(walPath)
	if err != nil {
		log.Printf("[batch:%s] WAL 压缩重写失败: %v", cb.name, err)
		cb.reopenWALLocked()
		return
	}
	w := bufio.NewWriterSize(f, ioBufSize)
	for _, line := range remaining {
		w.Write(line)
		w.WriteByte('\n')
	}
	w.Flush()

	cb.walFile = f
	cb.walWriter = bufio.NewWriterSize(f, ioBufSize)

	if len(remaining) > 0 {
		log.Printf("[batch:%s] WAL 已压缩，剩余 %d 条未处理", cb.name, len(remaining))
	}
}

func (cb *connBatch) reopenWALLocked() {
	f, err := os.OpenFile(cb.walPath(), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[batch:%s] WAL 重新打开失败: %v", cb.name, err)
		return
	}
	cb.walFile = f
	cb.walWriter = bufio.NewWriterSize(f, ioBufSize)
}

// recoverWAL 启动时从 WAL 恢复未完成的条目到实时队列。
// 已存在于重试状态文件中的条目不再重复恢复到实时队列。
func (cb *connBatch) recoverWAL(skipSeqs map[int64]bool) {
	path := cb.walPath()
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}

	// 收集 done 集合和所有 SQL 条目
	doneSet := make(map[int64]bool)
	var items []*SQLItem
	var maxSeq int64

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 {
			continue
		}
		// 检查 done 标记
		var donePeek struct {
			Done []int64 `json:"done"`
		}
		if json.Unmarshal([]byte(line), &donePeek) == nil && len(donePeek.Done) > 0 {
			for _, seq := range donePeek.Done {
				doneSet[seq] = true
			}
			continue
		}
		// 解析 SQL 条目
		var item SQLItem
		if json.Unmarshal([]byte(line), &item) == nil && item.SeqNo > 0 {
			items = append(items, &item)
			if item.SeqNo > maxSeq {
				maxSeq = item.SeqNo
			}
		}
	}

	// 将未完成的条目恢复到队列
	recovered := 0
	for _, item := range items {
		if !doneSet[item.SeqNo] {
			if skipSeqs != nil && skipSeqs[item.SeqNo] {
				continue
			}
			select {
			case cb.queue <- item:
				recovered++
			default:
				log.Printf("[batch:%s] ⚠️ WAL 恢复时队列已满，丢弃 seq=%d", cb.name, item.SeqNo)
			}
		}
	}

	cb.walMu.Lock()
	cb.walSeq = maxSeq
	cb.walMu.Unlock()

	if recovered > 0 {
		log.Printf("[batch:%s] ✅ 从 WAL 恢复了 %d 条未完成 SQL", cb.name, recovered)
	}
}

// ── 入队 ─────────────────────────────────────────────────────────────────────

// Enqueue 将 SQL 入队：替换 NOW()、写入 WAL、阻塞等待队列有空位。
// ctx 用于检测客户端断开（一般不会触发，SCADA 会等待）。
func (cb *connBatch) Enqueue(ctx context.Context, sqls []string) (int, error) {
	count := 0
	now := time.Now().Format("2006-01-02 15:04:05")

	for _, raw := range sqls {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		// 入队时替换 NOW() 为实际时间，确保队列积压不影响时间准确性
		s = nowRegex.ReplaceAllString(s, "'"+now+"'")

		item := &SQLItem{
			SQL:        s,
			Conn:       cb.name,
			EnqueuedAt: now,
		}

		// 先写 WAL 再入队（保证持久化优先于内存）
		cb.walAppend(item)

		// 阻塞式入队：队列满时等待空位
		select {
		case cb.queue <- item:
			count++
		case <-ctx.Done():
			cb.walFlush()
			return count, ctx.Err()
		case <-cb.ctx.Done():
			cb.walFlush()
			return count, fmt.Errorf("服务正在关闭")
		}
	}

	// 批量入队完成后统一 flush WAL 缓冲
	cb.walFlush()
	return count, nil
}

// ── 刷出循环 ─────────────────────────────────────────────────────────────────

// flushLoop 是核心消费循环：等待第一条数据 → 开启 500ms 窗口收集更多 → 批量执行。
// 同时每秒检查一次重试队列，处理到期的重试条目。
func (cb *connBatch) flushLoop() {
	defer cb.wg.Done()

	batch := make([]*SQLItem, 0, cb.cfg.FlushBatch)
	compactCounter := 0
	flushInterval := time.Duration(cb.cfg.FlushInterval) * time.Millisecond

	// 重试检查定时器（1秒一次，处理无新数据时的重试队列）
	retryCheck := time.NewTicker(time.Second)
	defer retryCheck.Stop()

	for {
		batch = batch[:0]

		// ── 阶段1：等待触发（新数据 或 重试检查） ──
		select {
		case <-cb.ctx.Done():
			return
		case item := <-cb.queue:
			batch = append(batch, item)
		case <-retryCheck.C:
			// 无新数据，仅处理到期的重试条目
			batch = cb.collectRetries(batch)
			if len(batch) == 0 {
				continue
			}
			cb.executeBatch(batch)
			compactCounter++
			if compactCounter >= 100 {
				compactCounter = 0
				cb.walCompact()
			}
			continue
		}

		// ── 阶段2：收到新数据，开启 500ms/FlushBatch 收集窗口 ──
		deadline := time.NewTimer(flushInterval)
	collectLoop:
		for len(batch) < cb.cfg.FlushBatch {
			select {
			case <-cb.ctx.Done():
				deadline.Stop()
				return
			case item := <-cb.queue:
				batch = append(batch, item)
			case <-deadline.C:
				break collectLoop
			}
		}
		deadline.Stop()

		// 顺便合并到期的重试条目
		batch = cb.collectRetries(batch)

		if len(batch) > 0 {
			cb.executeBatch(batch)
			compactCounter++
		}

		// 每 100 次批量执行做一次 WAL 压缩
		if compactCounter >= 100 {
			compactCounter = 0
			cb.walCompact()
		}
	}
}

// collectRetries 从重试队列中取出已到期的条目，追加到 batch 并返回
func (cb *connBatch) collectRetries(batch []*SQLItem) []*SQLItem {
	cb.retryMu.Lock()
	now := time.Now()
	var still []*SQLItem
	moved := false
	for _, item := range cb.retryQ {
		if now.After(item.nextRetry) {
			batch = append(batch, item)
			moved = true
		} else {
			still = append(still, item)
		}
	}
	cb.retryQ = still
	cb.retryMu.Unlock()

	cb.connRetryMu.Lock()
	var connStill []*SQLItem
	for _, item := range cb.connRetryQ {
		if now.After(item.nextRetry) {
			batch = append(batch, item)
			moved = true
		} else {
			connStill = append(connStill, item)
		}
	}
	cb.connRetryQ = connStill
	cb.connRetryMu.Unlock()

	if moved {
		cb.markRetryStateDirty()
	}
	return batch
}

// ── 批量执行 ─────────────────────────────────────────────────────────────────

// executeBatch 执行一批 SQL：先尝试事务批量提交，失败则降级逐条执行
func (cb *connBatch) executeBatch(items []*SQLItem) {
	cb.lastFlush.Store(time.Now().Format("15:04:05"))

	// 动态获取当前连接池（Reconnect 后能拿到新 Pool）
	pool := cb.manager.GetPool(cb.name)
	if pool == nil || !pool.IsConnected() {
		log.Printf("[batch:%s] ⚠️ 数据库未连接，%d 条 SQL 进入长期重试队列", cb.name, len(items))
		for _, item := range items {
			cb.sendToConnRetry(item, ErrNotConnected.Error())
		}
		return
	}
	rawDB := pool.DB()
	if rawDB == nil {
		for _, item := range items {
			cb.sendToConnRetry(item, ErrNotConnected.Error())
		}
		return
	}

	// 尝试事务批量执行（最快路径）
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	tx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		if isDisconnectError(err) {
			log.Printf("[batch:%s] ⚠️ 开启事务失败: %v，%d 条进入长期重试队列", cb.name, err, len(items))
			for _, item := range items {
				cb.sendToConnRetry(item, err.Error())
			}
			return
		}
		if isTransientError(err) {
			log.Printf("[batch:%s] ⚠️ 开启事务失败: %v，%d 条进入重试队列", cb.name, err, len(items))
			for _, item := range items {
				cb.sendToNormalRetry(item, err.Error())
			}
			return
		}
		log.Printf("[batch:%s] ⚠️ 开启事务失败: %v，%d 条降级逐条执行", cb.name, err, len(items))
		cb.execOneByOne(rawDB, items)
		return
	}

	for _, item := range items {
		if _, err := tx.ExecContext(ctx, item.SQL); err != nil {
			_ = tx.Rollback()
			if isDisconnectError(err) {
				log.Printf("[batch:%s] ⚠️ 事务内执行失败: %v，整批进入长期重试", cb.name, err)
				for _, retryItem := range items {
					cb.sendToConnRetry(retryItem, err.Error())
				}
				return
			}
			log.Printf("[batch:%s] ⚠️ 事务内执行失败: %v，降级逐条执行", cb.name, err)
			cb.execOneByOne(rawDB, items)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		if isDisconnectError(err) {
			log.Printf("[batch:%s] ⚠️ 事务提交失败: %v，整批进入长期重试", cb.name, err)
			for _, item := range items {
				cb.sendToConnRetry(item, err.Error())
			}
			return
		}
		log.Printf("[batch:%s] ⚠️ 事务提交失败: %v，降级逐条执行", cb.name, err)
		cb.execOneByOne(rawDB, items)
		return
	}

	// 事务全部成功
	cb.totalProcessed.Add(int64(len(items)))
	var doneSeqs []int64
	for _, item := range items {
		if item.SeqNo > 0 {
			doneSeqs = append(doneSeqs, item.SeqNo)
		}
	}
	cb.walMarkDone(doneSeqs)
	log.Printf("[batch:%s] ✅ 批量执行成功 %d 条%s", cb.name, len(items), batchSummary(items))
}

// batchSummary 从一批 SQL 中提取目标表摘要（轻量字符串操作，不影响性能）
func batchSummary(items []*SQLItem) string {
	tables := make(map[string]int, 4)
	for _, item := range items {
		s := strings.ToUpper(item.SQL)
		raw := item.SQL
		var tbl string
		if idx := strings.Index(s, "INTO "); idx >= 0 {
			// INSERT INTO table_name
			rest := raw[idx+5:]
			if sp := strings.IndexAny(rest, " (\t\n"); sp > 0 {
				tbl = rest[:sp]
			}
		} else if idx := strings.Index(s, "UPDATE "); idx >= 0 {
			rest := raw[idx+7:]
			if sp := strings.IndexAny(rest, " \t\n"); sp > 0 {
				tbl = rest[:sp]
			}
		}
		if tbl != "" {
			tables[tbl]++
		}
	}
	if len(tables) == 0 {
		return ""
	}
	var parts []string
	for t, c := range tables {
		parts = append(parts, fmt.Sprintf("%s×%d", t, c))
	}
	return " → " + strings.Join(parts, ", ")
}

// execOneByOne 逐条执行，成功的标记 WAL，失败的送入重试或死信
func (cb *connBatch) execOneByOne(rawDB *sql.DB, items []*SQLItem) {
	var doneSeqs []int64
	successCount := 0

	for _, item := range items {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		_, err := rawDB.ExecContext(ctx, item.SQL)
		cancel()

		if err != nil {
			item.LastError = err.Error()
			if isTransientError(err) {
				cb.sendToRetry(item, err)
			} else {
				// 确定性失败（语法错误、约束冲突等），直接进死信
				cb.sendToDead(item, err.Error())
			}
		} else {
			successCount++
			if item.SeqNo > 0 {
				doneSeqs = append(doneSeqs, item.SeqNo)
			}
		}
	}

	if successCount > 0 {
		cb.totalProcessed.Add(int64(successCount))
		cb.walMarkDone(doneSeqs)
		log.Printf("[batch:%s] 逐条执行完成 %d/%d 成功%s", cb.name, successCount, len(items), batchSummary(items))
	}
}

// ── 重试与死信 ───────────────────────────────────────────────────────────────

func (cb *connBatch) connRetryBackoff(retries int) time.Duration {
	if retries <= 0 {
		retries = 1
	}
	backoff := cb.cfg.ConnRetryBaseInterval
	for i := 1; i < retries; i++ {
		if backoff >= cb.cfg.ConnRetryMaxInterval {
			backoff = cb.cfg.ConnRetryMaxInterval
			break
		}
		backoff *= 2
		if backoff > cb.cfg.ConnRetryMaxInterval {
			backoff = cb.cfg.ConnRetryMaxInterval
		}
	}
	return time.Duration(backoff) * time.Millisecond
}

// sendToRetry 按错误类别将条目送入普通重试、长期重试或死信。
func (cb *connBatch) sendToRetry(item *SQLItem, err error) {
	if err != nil {
		item.LastError = err.Error()
	}
	if isDisconnectError(err) {
		cb.sendToConnRetry(item, item.LastError)
		return
	}
	if isTransientError(err) {
		cb.sendToNormalRetry(item, item.LastError)
		return
	}
	cb.sendToDead(item, item.LastError)
}

func (cb *connBatch) sendToNormalRetry(item *SQLItem, errMsg string) {
	markRetryStart(item)
	item.LastError = errMsg
	item.RetryKind = retryKindNormal
	item.Retries++
	if item.Retries > cb.cfg.MaxRetries {
		cb.sendToDead(item, item.LastError)
		return
	}
	backoff := time.Duration(1<<(item.Retries-1)) * time.Second
	setItemNextRetry(item, time.Now().Add(backoff))

	cb.retryMu.Lock()
	cb.retryQ = append(cb.retryQ, item)
	cb.retryMu.Unlock()
	cb.markRetryStateDirty()

	log.Printf("[batch:%s] SQL 进入重试队列 (第%d次): %s | 错误: %s", cb.name, item.Retries, truncateSQL(item.SQL), errMsg)
}

func (cb *connBatch) sendToConnRetry(item *SQLItem, errMsg string) {
	markRetryStart(item)
	item.LastError = errMsg
	item.RetryKind = retryKindConn
	item.ConnRetries++
	backoff := cb.connRetryBackoff(item.ConnRetries)
	setItemNextRetry(item, time.Now().Add(backoff))

	cb.connRetryMu.Lock()
	cb.connRetryQ = append(cb.connRetryQ, item)
	cb.connRetryMu.Unlock()
	cb.markRetryStateDirty()

	log.Printf("[batch:%s] SQL 进入长期重试队列 (第%d次，%s后重试): %s | 错误: %s", cb.name, item.ConnRetries, backoff, truncateSQL(item.SQL), errMsg)
}

// sendToDead 将条目送入死信队列（内存 + 文件），并在 WAL 中标记完成
func (cb *connBatch) sendToDead(item *SQLItem, errMsg string) {
	dead := DeadItem{
		SQL:        item.SQL,
		Conn:       item.Conn,
		Error:      errMsg,
		Retries:    item.Retries,
		EnqueuedAt: item.EnqueuedAt,
		FailedAt:   time.Now().Format("2006-01-02 15:04:05"),
	}

	// 内存保留最近 N 条
	cb.deadMu.Lock()
	cb.deadQ = append(cb.deadQ, dead)
	if len(cb.deadQ) > cb.cfg.DeadMemLimit {
		cb.deadQ = cb.deadQ[len(cb.deadQ)-cb.cfg.DeadMemLimit:]
	}
	cb.deadCount++
	cb.deadMu.Unlock()

	// 写入死信文件（每次独立打开，自动处理日期滚动）
	cb.writeDeadFile(dead)

	// 死信也标记 WAL 完成（不再重试，不需要崩溃恢复）
	if item.SeqNo > 0 {
		cb.walMarkDone([]int64{item.SeqNo})
	}

	cb.totalFailed.Add(1)
	log.Printf("[batch:%s] ❌ SQL 进入死信队列: %s | 错误: %s", cb.name, truncateSQL(item.SQL), errMsg)
}

// writeDeadFile 追加写入死信缓冲（常驻句柄 + 大缓冲，由 ioFlushLoop 定时刷盘）
func (cb *connBatch) writeDeadFile(dead DeadItem) {
	cb.deadMu.Lock()
	defer cb.deadMu.Unlock()

	// 检查日期滚动
	cb.openDeadFileLocked()
	if cb.deadWriter == nil {
		return
	}
	data, _ := json.Marshal(dead)
	cb.deadWriter.Write(data)
	cb.deadWriter.WriteByte('\n')
	// 不 Flush — 由 ioFlushLoop 定时刷盘
}

// ── I/O 刷盘协程 ─────────────────────────────────────────────────────────────

// ioFlushLoop 每 2 秒统一刷出 WAL 和死信文件的缓冲到磁盘。
// 将多次零碎写入合并为一次顺序写入，大幅减少机械硬盘的寻道次数。
func (cb *connBatch) ioFlushLoop() {
	defer cb.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastRetryPersist := time.Now()
	persistInterval := time.Duration(cb.cfg.RetryStatePersistInterval) * time.Millisecond

	for {
		select {
		case <-cb.ctx.Done():
			// 关闭前最后一次刷盘
			cb.flushAllBuffers()
			cb.persistRetryState()
			return
		case <-ticker.C:
			cb.flushAllBuffers()
			if cb.retryStateDirty.Load() && time.Since(lastRetryPersist) >= persistInterval {
				cb.persistRetryState()
				lastRetryPersist = time.Now()
			}
		}
	}
}

// flushAllBuffers 统一刷出 WAL 和死信文件缓冲
func (cb *connBatch) flushAllBuffers() {
	cb.walMu.Lock()
	if cb.walWriter != nil {
		cb.walWriter.Flush()
	}
	cb.walMu.Unlock()

	cb.deadMu.Lock()
	if cb.deadWriter != nil {
		cb.deadWriter.Flush()
	}
	cb.deadMu.Unlock()
}

// ── 状态查询 ─────────────────────────────────────────────────────────────────

// Stats 返回当前队列统计信息
func (cb *connBatch) Stats() BatchStats {
	cb.retryMu.Lock()
	retrySize := len(cb.retryQ)
	retryQ := make([]*SQLItem, len(cb.retryQ))
	copy(retryQ, cb.retryQ)
	cb.retryMu.Unlock()

	cb.connRetryMu.Lock()
	connRetrySize := len(cb.connRetryQ)
	connRetryQ := make([]*SQLItem, len(cb.connRetryQ))
	copy(connRetryQ, cb.connRetryQ)
	cb.connRetryMu.Unlock()

	cb.deadMu.Lock()
	deadCount := cb.deadCount
	cb.deadMu.Unlock()

	lf, _ := cb.lastFlush.Load().(string)
	oldestRetryAt := ""
	lastRetryError := ""
	oldestRetryTime := time.Time{}
	for _, item := range append(retryQ, connRetryQ...) {
		if item == nil {
			continue
		}
		if lastRetryError == "" && item.LastError != "" {
			lastRetryError = item.LastError
		}
		candidate := parseStoredTime(item.FirstRetryAt)
		if candidate.IsZero() {
			candidate = parseStoredTime(item.EnqueuedAt)
		}
		if candidate.IsZero() {
			continue
		}
		if oldestRetryTime.IsZero() || candidate.Before(oldestRetryTime) {
			oldestRetryTime = candidate
			oldestRetryAt = candidate.Format(timeLayout)
		}
	}

	return BatchStats{
		Conn:           cb.name,
		QueueSize:      len(cb.queue),
		QueueCapacity:  cap(cb.queue),
		RetrySize:      retrySize,
		ConnRetrySize:  connRetrySize,
		DeadLetterSize: deadCount,
		TotalProcessed: cb.totalProcessed.Load(),
		TotalFailed:    cb.totalFailed.Load(),
		LastFlush:      lf,
		OldestRetryAt:  oldestRetryAt,
		LastRetryError: lastRetryError,
		RetryFileSize:  cb.retryStateFileSize(),
	}
}

// DeadLetters 返回内存中的死信列表（最近 N 条）
func (cb *connBatch) DeadLetters() []DeadItem {
	cb.deadMu.Lock()
	defer cb.deadMu.Unlock()
	out := make([]DeadItem, len(cb.deadQ))
	copy(out, cb.deadQ)
	return out
}

// ── 优雅关闭 ─────────────────────────────────────────────────────────────────

// Drain 排空队列：停止消费循环 → 取出剩余 → 尝试执行 → 持久化失败的
func (cb *connBatch) Drain() {
	log.Printf("[batch:%s] 正在排空队列...", cb.name)

	// 停止 flushLoop
	cb.cancel()
	cb.wg.Wait()

	// 收集实时队列中剩余的
	var remaining []*SQLItem
drainQueue:
	for {
		select {
		case item := <-cb.queue:
			remaining = append(remaining, item)
		default:
			break drainQueue
		}
	}

	// 收集重试队列
	cb.retryMu.Lock()
	remaining = append(remaining, cb.retryQ...)
	cb.retryQ = nil
	cb.retryMu.Unlock()

	// 收集长期重试队列
	cb.connRetryMu.Lock()
	remaining = append(remaining, cb.connRetryQ...)
	cb.connRetryQ = nil
	cb.connRetryMu.Unlock()
	cb.markRetryStateDirty()

	if len(remaining) > 0 {
		log.Printf("[batch:%s] 排空中，剩余 %d 条待执行", cb.name, len(remaining))
		pool := cb.manager.GetPool(cb.name)
		if pool != nil && pool.IsConnected() {
			rawDB := pool.DB()
			if rawDB != nil {
				cb.execOneByOne(rawDB, remaining)
			} else {
				cb.persistRemaining(remaining)
			}
		} else {
			// 数据库不可用，将未执行的持久化到 WAL（下次启动恢复）
			cb.persistRemaining(remaining)
		}
	}
	cb.persistRetryState()

	// 关闭 WAL 文件
	cb.walMu.Lock()
	if cb.walWriter != nil {
		cb.walWriter.Flush()
	}
	if cb.walFile != nil {
		cb.walFile.Close()
	}
	cb.walMu.Unlock()

	// 关闭死信文件
	cb.deadMu.Lock()
	if cb.deadWriter != nil {
		cb.deadWriter.Flush()
	}
	if cb.deadFile != nil {
		cb.deadFile.Close()
		cb.deadFile = nil
		cb.deadWriter = nil
	}
	cb.deadMu.Unlock()

	log.Printf("[batch:%s] 队列已排空", cb.name)
}

// persistRemaining 将未执行的 SQL 持久化到 WAL（程序关闭时数据库不可用的应急方案）
func (cb *connBatch) persistRemaining(items []*SQLItem) {
	cb.walMu.Lock()
	defer cb.walMu.Unlock()

	if cb.walWriter == nil {
		return
	}
	for _, item := range items {
		if item.SeqNo != 0 {
			continue
		}
		cb.walSeq++
		item.SeqNo = cb.walSeq
		data, _ := json.Marshal(item)
		cb.walWriter.Write(data)
		cb.walWriter.WriteByte('\n')
	}
	cb.walWriter.Flush()
	log.Printf("[batch:%s] 已将 %d 条未执行 SQL 持久化到 WAL", cb.name, len(items))
}

// ── BatchExecutor: 管理所有连接的批量执行器 ──────────────────────────────────

// BatchExecutor 统一管理所有连接的批量执行上下文
type BatchExecutor struct {
	cfg     BatchConfig
	manager *Manager
	dataDir string

	mu      sync.RWMutex
	batches map[string]*connBatch
}

// NewBatchExecutor 创建批量执行器，为每个已有连接初始化独立队列
func NewBatchExecutor(cfg BatchConfig, mgr *Manager, dataDir string) *BatchExecutor {
	cfg.Normalize()
	_ = os.MkdirAll(dataDir, 0755)

	be := &BatchExecutor{
		cfg:     cfg,
		manager: mgr,
		dataDir: dataDir,
		batches: make(map[string]*connBatch),
	}

	// 为已有的每个连接初始化 connBatch
	for _, status := range mgr.ListStatus() {
		pool := mgr.GetPool(status.Name)
		if pool != nil {
			be.batches[status.Name] = newConnBatch(status.Name, mgr, cfg, dataDir)
		}
	}

	log.Printf("[batch] 批量执行器已启动，队列容量: %d，刷出间隔: %dms，批次上限: %d",
		cfg.QueueSize, cfg.FlushInterval, cfg.FlushBatch)
	return be
}

// getOrCreate 获取或按需创建连接的 connBatch（线程安全）
func (be *BatchExecutor) getOrCreate(connName string) *connBatch {
	be.mu.RLock()
	cb := be.batches[connName]
	be.mu.RUnlock()
	if cb != nil {
		return cb
	}

	be.mu.Lock()
	defer be.mu.Unlock()
	// double-check
	if cb = be.batches[connName]; cb != nil {
		return cb
	}

	pool := be.manager.poolByName(connName)
	if pool == nil {
		return nil
	}
	cb = newConnBatch(connName, be.manager, be.cfg, be.dataDir)
	be.batches[connName] = cb
	return cb
}

// Enqueue 将 SQL 入队到指定连接的批量队列
func (be *BatchExecutor) Enqueue(ctx context.Context, connName string, sqls []string) (int, error) {
	if connName == "" {
		connName = "default"
	}
	cb := be.getOrCreate(connName)
	if cb == nil {
		return 0, fmt.Errorf("连接 '%s' 不存在", connName)
	}
	return cb.Enqueue(ctx, sqls)
}

// AllStats 返回所有连接的队列统计
func (be *BatchExecutor) AllStats() []BatchStats {
	be.mu.RLock()
	defer be.mu.RUnlock()
	out := make([]BatchStats, 0, len(be.batches))
	for _, cb := range be.batches {
		out = append(out, cb.Stats())
	}
	return out
}

// DeadLetters 返回指定连接的死信列表（内存中的最近 N 条）
func (be *BatchExecutor) DeadLetters(connName string) []DeadItem {
	be.mu.RLock()
	cb := be.batches[connName]
	be.mu.RUnlock()
	if cb == nil {
		return nil
	}
	return cb.DeadLetters()
}

// Close 优雅关闭：排空所有连接的队列（必须在 Pool 关闭之前调用）
func (be *BatchExecutor) Close() {
	be.mu.Lock()
	defer be.mu.Unlock()
	for name, cb := range be.batches {
		cb.Drain()
		delete(be.batches, name)
	}
	log.Printf("[batch] 批量执行器已关闭")
}
