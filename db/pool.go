// Package db 封装数据库连接池（MySQL + ODBC），提供健壮的断线无限重试机制和高并发支持。
//
// 设计要点：
//   - 使用标准库 database/sql，支持 mysql 与 odbc 驱动，无 ORM
//   - 后台 watchdog goroutine 以指数退避持续重连，调用方无需关心连接是否可用
//   - 连接池参数针对高并发场景调优
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/alexbrainman/odbc"
	_ "github.com/go-sql-driver/mysql"
)

// ── 常量 ─────────────────────────────────────────────────────────────────────

const (
	backoffInit = 1 * time.Second  // 初始退避间隔
	backoffMax  = 32 * time.Second // 最大退避间隔

	// 连接池参数
	maxOpen     = 30             // 最大打开连接数
	maxIdle     = 10             // 最大空闲连接数
	connMaxLife = 5 * time.Minute // 连接最大存活时间

	// 健康检查间隔
	pingInterval = 15 * time.Second

	// 单次查询最长等待时间
	queryTimeout = 30 * time.Second
)

// ── 连接池 ───────────────────────────────────────────────────────────────────

// Pool 封装 *sql.DB，并内置自动重连 watchdog。支持 MySQL 与 ODBC。
type Pool struct {
	cfg ConnConfig

	mu          sync.RWMutex
	db          *sql.DB
	isConnected atomic.Bool // 0=断开 1=已连接

	ctx    context.Context
	cancel context.CancelFunc
}

// New 创建连接池并启动后台 watchdog，立刻尝试首次连接。
// 即使首次连接失败，watchdog 也会持续重试，调用方无需等待。
func New(cfg ConnConfig) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{cfg: cfg, ctx: ctx, cancel: cancel}
	go p.watchdog()
	return p
}

// Close 关闭连接池并停止 watchdog。
func (p *Pool) Close() {
	p.cancel()
	p.mu.Lock()
	if p.db != nil {
		_ = p.db.Close()
	}
	p.mu.Unlock()
	log.Printf("[db] 连接池已关闭")
}

// IsConnected 返回当前连接状态。
func (p *Pool) IsConnected() bool {
	return p.isConnected.Load()
}

// GetStatus 返回该连接的对外状态（名称、类型、是否已连接、可读描述）。
func (p *Pool) GetStatus() ConnStatus {
	p.cfg.Normalize()
	return ConnStatus{
		Name:        p.cfg.Name,
		Type:        p.cfg.Type,
		IsConnected: p.isConnected.Load(),
		Display:     p.cfg.Display(),
		IsKH:        p.cfg.IsKH,
	}
}

// IsKH 返回该连接是否为 KH 工业库模式。
func (p *Pool) IsKH() bool {
	return p.cfg.IsKH
}

// Config 返回连接配置（只读），用于重连等。
func (p *Pool) Config() ConnConfig {
	return p.cfg
}

// DB 返回底层 *sql.DB，可能为 nil（断开时）。
// 调用方应检查返回值，使用 QueryContext / ExecContext 配合超时 context。
func (p *Pool) DB() *sql.DB {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.db
}

// ── watchdog ─────────────────────────────────────────────────────────────────

// watchdog 以指数退避永久保持连接活跃。
// 首次连接 + 定期 ping + 断线后重连，三合一。
func (p *Pool) watchdog() {
	backoff := backoffInit

	for {
		// 尝试（重新）连接
		if err := p.connect(); err != nil {
			p.isConnected.Store(false)
			log.Printf("[db] 连接失败，%s 后重试: %v", backoff, err)
			select {
			case <-p.ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}

		// 连接成功，重置退避
		backoff = backoffInit
		p.isConnected.Store(true)
		log.Printf("[db] 已连接 [%s] %s", p.cfg.Name, p.cfg.Display())

		// 定期 ping 检测连接存活
		if err := p.keepAlive(); err != nil {
			p.isConnected.Store(false)
			log.Printf("[db] 连接中断，触发重连: %v", err)
			// 关闭旧连接，进入下一轮重连
			p.mu.Lock()
			if p.db != nil {
				_ = p.db.Close()
				p.db = nil
			}
			p.mu.Unlock()
		}

		// 检查是否已要求关闭
		select {
		case <-p.ctx.Done():
			return
		default:
		}
	}
}

// connect 打开并验证一个新的 *sql.DB（MySQL 或 ODBC）。
func (p *Pool) connect() error {
	driver, dsn := p.cfg.driverDSN()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return fmt.Errorf("sql.Open(%s) 失败: %w", driver, err)
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(connMaxLife)

	// 立即验证连接
	pingCtx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	if err = db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping 失败: %w", err)
	}

	p.mu.Lock()
	if p.db != nil {
		_ = p.db.Close()
	}
	p.db = db
	p.mu.Unlock()
	return nil
}

// keepAlive 定期 ping 直到失败或 ctx 取消。
func (p *Pool) keepAlive() error {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return nil // 正常关闭
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
			p.mu.RLock()
			db := p.db
			p.mu.RUnlock()
			err := db.PingContext(pingCtx)
			cancel()
			if err != nil {
				return fmt.Errorf("定期 ping 失败: %w", err)
			}
		}
	}
}

// ── 查询辅助 ─────────────────────────────────────────────────────────────────

// ErrNotConnected 表示当前数据库未连接。
var ErrNotConnected = fmt.Errorf("数据库未连接，正在重试中")

// acquire 获取当前可用的 *sql.DB，未连接时返回 ErrNotConnected。
func (p *Pool) acquire() (*sql.DB, error) {
	if !p.isConnected.Load() {
		return nil, ErrNotConnected
	}
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()
	if db == nil {
		return nil, ErrNotConnected
	}
	return db, nil
}

// QueryResult 统一的查询结果结构。
type QueryResult struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Count   int              `json:"count"`
}

// ExecResult 统一的执行结果结构（INSERT/UPDATE/DELETE）。
type ExecResult struct {
	AffectedRows int64 `json:"affected_rows"`
	LastInsertID int64 `json:"last_insert_id,omitempty"`
}

// RunSQL 执行任意 SQL 语句，自动区分查询与修改。
// 对应 Python query_sql.py 的 run_sql 函数。
func (p *Pool) RunSQL(sqlStr string) (any, error) {
	db, err := p.acquire()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	// 先尝试作为查询执行
	rows, err := db.QueryContext(ctx, sqlStr)
	if err != nil {
		// 区分语法错误 vs 运行时错误，直接透传错误信息
		return nil, fmt.Errorf("SQL 执行失败: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("获取列名失败: %w", err)
	}

	// 返回行数据（SELECT / WITH ... SELECT 等）
	result := QueryResult{Columns: cols}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("扫描行数据失败: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			// []byte → string（避免 JSON 序列化为 base64）
			if b, ok := vals[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = vals[i]
			}
		}
		result.Rows = append(result.Rows, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历结果集失败: %w", err)
	}
	result.Count = len(result.Rows)
	return result, nil
}

// RunKH 将整段 SQL（含多条 SET + SELECT）一次性发给 KH 工业库驱动执行。
// KH 驱动通过 SQLExecDirect 自行解析多条语句，最终返回最后一个结果集。
// 调用方不需要拆分语句，直接把原始脚本传入即可。
func (p *Pool) RunKH(sqlStr string) (any, error) {
	db, err := p.acquire()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("KH SQL 执行失败: %w", err)
	}
	defer rows.Close()

	// 跳过无列的中间结果集（SET 语句产生的空结果），找到有列的那个
	for {
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("获取列名失败: %w", err)
		}
		if len(cols) > 0 {
			// 找到有数据的结果集，读取行
			result := QueryResult{Columns: cols}
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err = rows.Scan(ptrs...); err != nil {
					return nil, fmt.Errorf("扫描行数据失败: %w", err)
				}
				row := make(map[string]any, len(cols))
				for i, col := range cols {
					if b, ok := vals[i].([]byte); ok {
						row[col] = string(b)
					} else {
						row[col] = vals[i]
					}
				}
				result.Rows = append(result.Rows, row)
			}
			if err = rows.Err(); err != nil {
				return nil, fmt.Errorf("遍历结果集失败: %w", err)
			}
			result.Count = len(result.Rows)
			return result, nil
		}
		// 当前结果集无列（SET 语句），尝试下一个结果集
		if !rows.NextResultSet() {
			break
		}
	}

	// 所有结果集均无列（纯 SET 脚本），返回空
	return QueryResult{Columns: []string{}, Rows: []map[string]any{}, Count: 0}, nil
}

// ExecSQL 执行不返回行的 SQL（INSERT/UPDATE/DELETE/DDL）。
func (p *Pool) ExecSQL(sqlStr string) (ExecResult, error) {
	db, err := p.acquire()
	if err != nil {
		return ExecResult{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	res, err := db.ExecContext(ctx, sqlStr)
	if err != nil {
		return ExecResult{}, fmt.Errorf("SQL 执行失败: %w", err)
	}
	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return ExecResult{AffectedRows: affected, LastInsertID: lastID}, nil
}

// ── 辅助 ─────────────────────────────────────────────────────────────────────

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
