// Package db 多数据源配置与状态结构。
package db

import (
	"fmt"
	"strings"
)

// ConnConfig 单条数据库连接配置，支持 MySQL 与 ODBC。
// 若 Type 为空则视为 "mysql"；Name 为空则视为 "default"。
type ConnConfig struct {
	Name     string `toml:"name"     json:"name"`
	Type     string `toml:"type"     json:"type"` // "mysql" | "odbc"
	Host     string `toml:"host"     json:"host"`
	Port     int    `toml:"port"     json:"port"`
	DBName   string `toml:"dbname"   json:"dbname"`
	User     string `toml:"user"     json:"user"`
	Password string `toml:"password" json:"password"`
	DSN      string `toml:"dsn"      json:"dsn"`   // ODBC 数据源名，仅 type=odbc 时使用
	IsKH     bool   `toml:"is_kh"    json:"is_kh"` // KH 工业库模式：整段 SQL 一次发送，驱动自行解析多条语句
}

// ConnStatus 单条连接的对外状态，供前端与 HTTP 展示。
type ConnStatus struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	IsConnected bool   `json:"is_connected"`
	Display     string `json:"display"` // 可读描述，如 "127.0.0.1:3306/sy" 或 "odbc:mtznh"
	IsKH        bool   `json:"is_kh"`
}

// Normalize 填充默认值。
func (c *ConnConfig) Normalize() {
	if c.Name == "" {
		c.Name = "default"
	}
	if c.Type == "" {
		c.Type = "mysql"
	}
}

// driverDSN 返回 (driverName, dsn) 用于 sql.Open。
func (c *ConnConfig) driverDSN() (driver string, dsn string) {
	c.Normalize()
	switch c.Type {
	case "odbc":
		driver = "odbc"
		dsn = c.DSN
		if dsn != "" && !strings.Contains(dsn, "=") {
			dsn = "DSN=" + dsn
		}
		if c.User != "" {
			dsn = dsn + ";UID=" + c.User + ";PWD=" + c.Password
		}
		return driver, dsn
	default:
		driver = "mysql"
		dsn = fmt.Sprintf(
			"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local&timeout=10s&readTimeout=30s&writeTimeout=30s&interpolateParams=true&maxAllowedPacket=16777216",
			c.User, c.Password, c.Host, c.Port, c.DBName,
		)
		return driver, dsn
	}
}

// Display 返回可读连接描述。
func (c *ConnConfig) Display() string {
	c.Normalize()
	if c.Type == "odbc" {
		return "odbc:" + c.DSN
	}
	return fmt.Sprintf("%s:%d/%s", c.Host, c.Port, c.DBName)
}

// BatchConfig 批量执行器配置
type BatchConfig struct {
	QueueSize                 int `toml:"queue_size"                  json:"queue_size"`                    // 实时队列容量，默认 100000
	FlushInterval             int `toml:"flush_interval"              json:"flush_interval"`                // 刷出间隔(ms)，默认 500
	FlushBatch                int `toml:"flush_batch"                 json:"flush_batch"`                   // 每批最大条数，默认 50
	MaxRetries                int `toml:"max_retries"                 json:"max_retries"`                   // 普通瞬态错误最大重试次数，默认 3
	DeadMemLimit              int `toml:"dead_mem_limit"              json:"dead_mem_limit"`                // 死信内存保留条数，默认 1000
	ConnRetryBaseInterval     int `toml:"conn_retry_base_interval"    json:"conn_retry_base_interval"`      // 断连类错误初始退避(ms)，默认 5000
	ConnRetryMaxInterval      int `toml:"conn_retry_max_interval"     json:"conn_retry_max_interval"`       // 断连类错误最大退避(ms)，默认 300000
	RetryStatePersistInterval int `toml:"retry_state_persist_interval" json:"retry_state_persist_interval"` // 重试状态快照落盘间隔(ms)，默认 10000
}

// Normalize 填充 BatchConfig 默认值
func (c *BatchConfig) Normalize() {
	if c.QueueSize <= 0 {
		c.QueueSize = 100000
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 500
	}
	if c.FlushBatch <= 0 {
		c.FlushBatch = 50
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.DeadMemLimit <= 0 {
		c.DeadMemLimit = 1000
	}
	if c.ConnRetryBaseInterval <= 0 {
		c.ConnRetryBaseInterval = 5000
	}
	if c.ConnRetryMaxInterval <= 0 {
		c.ConnRetryMaxInterval = 300000
	}
	if c.ConnRetryMaxInterval < c.ConnRetryBaseInterval {
		c.ConnRetryMaxInterval = c.ConnRetryBaseInterval
	}
	if c.RetryStatePersistInterval <= 0 {
		c.RetryStatePersistInterval = 10000
	}
}
