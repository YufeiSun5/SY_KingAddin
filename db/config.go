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
	Type     string `toml:"type"     json:"type"`     // "mysql" | "odbc"
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
			"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local&timeout=10s&readTimeout=30s&writeTimeout=30s",
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
