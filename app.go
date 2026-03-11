package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"temp_init/db"
	"temp_init/logger"
	"temp_init/menu"
	"temp_init/scada"

	"github.com/BurntSushi/toml"
	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows/registry"
)

// App struct
type App struct {
	ctx        context.Context
	configPath string         // 配置文件绝对路径
	scada      *scada.Client  // SCADA 客户端单例
	menu       *menu.Manager  // 菜单管理器单例
	db         *db.Manager    // 多数据库连接管理器
	httpServer *http.Server   // 对外暴露的 HTTP 插件服务
}

// AppConfig 对应 config.toml 的完整结构
type AppConfig struct {
	MySQL     MysqlConfig      `toml:"mysql"     json:"mysql"`
	Databases []db.ConnConfig  `toml:"databases" json:"databases"` // 多连接，优先于 mysql
	API       APIConfig        `toml:"api"       json:"api"`
	SCADA     ScadaConfig      `toml:"scada"     json:"scada"`
}

// MysqlConfig MySQL 连接参数
type MysqlConfig struct {
	Host     string `toml:"host"     json:"host"`
	Port     int    `toml:"port"     json:"port"`
	DBName   string `toml:"dbname"   json:"dbname"`
	User     string `toml:"user"     json:"user"`
	Password string `toml:"password" json:"password"`
}

// APIConfig FastAPI 服务参数
type APIConfig struct {
	Host string `toml:"host"  json:"host"`
	Port int    `toml:"port"  json:"port"`
	MyIP string `toml:"my_ip" json:"my_ip"`
}

// ScadaConfig SCADA 接口参数
type ScadaConfig struct {
	BaseURL  string `toml:"base_url"  json:"base_url"`
	Username string `toml:"username"  json:"username"`
	Password string `toml:"password"  json:"password"`
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup 在 Wails 窗口显示前调用
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 定位 exe 目录，开发模式下 wails dev 的 exe 在 build/bin/，
	// 但 config.toml 在项目根目录，逐级向上查找直到找到为止
	exe, err := os.Executable()
	if err != nil {
		exe = "."
	}
	exeDir := filepath.Dir(exe)
	a.configPath = findConfigToml(exeDir)

	// 初始化日志系统（必须最先初始化，后续所有 log.Printf 都会被捕获）
	logDir := filepath.Join(exeDir, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logger.Init(ctx, logDir)

	log.Printf("🚀 GOKS 插件服务启动中...")

	// 读取配置
	cfg, err := a.loadConfig()
	if err != nil {
		log.Printf("⚠️ 读取配置失败，使用内置默认值: %v", err)
		cfg = defaultConfig()
	} else {
		log.Printf("✅ 配置加载成功: %s", a.configPath)
		// 广播配置信息给前端（窗口就绪后前端会收到）
		go func() {
			runtime.EventsEmit(a.ctx, "config:loaded", cfg)
		}()
	}

	// 初始化各子系统
	a.startSubsystems(cfg)

	// 系统托盘（独立 goroutine，阻塞式运行）
	go systray.Run(a.onTrayReady, a.onTrayExit)
}

// startSubsystems 根据配置启动/重启所有子系统
func (a *App) startSubsystems(cfg *AppConfig) {
	// 初始化 SCADA 客户端
	a.scada = scada.New(scada.Config{
		BaseURL:  cfg.SCADA.BaseURL,
		Username: cfg.SCADA.Username,
		Password: cfg.SCADA.Password,
	})
	a.scada.Start()

	// 首次登录（异步）
	go func() {
		if err := a.scada.Login(false); err != nil {
			log.Printf("⚠️ 首次 SCADA 登录失败: %v", err)
		}
	}()

	// 初始化菜单管理器
	a.menu = menu.New(func(varName, value string) error {
		return a.scada.WriteVariables([]scada.WriteItem{{N: varName, V: value}})
	})

	// 初始化多数据库连接（后台每条连接独立 watchdog 断线重连）
	dbConfigs := dbConnConfigs(cfg)
	a.db = db.NewManager(dbConfigs)

	// 构建 HTTP 路由
	mux := http.NewServeMux()
	a.scada.RegisterRoutes(mux, "/api/scada")
	a.menu.RegisterRoutes(mux, "/api/menu")
	a.db.RegisterRoutes(mux, "/api/db")

	port := ":" + strconv.Itoa(cfg.API.Port)
	addr := cfg.API.MyIP + ":" + strconv.Itoa(cfg.API.Port)
	a.httpServer = &http.Server{Addr: port, Handler: mux}
	go func() {
		log.Printf("🚀 HTTP 插件服务就绪: http://%s", addr)
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ HTTP 服务异常退出: %v", err)
		}
	}()
}

// onTrayReady 初始化托盘图标和菜单
func (a *App) onTrayReady() {
	systray.SetTitle("GOKS")
	systray.SetTooltip("GOKS 插件服务运行中")

	mShow := systray.AddMenuItem("显示窗口", "打开日志窗口")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出 GOKS", "彻底退出程序")

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				runtime.WindowShow(a.ctx)
			case <-mQuit.ClickedCh:
				systray.Quit()
				runtime.Quit(a.ctx)
				return
			}
		}
	}()
}

// onTrayExit 托盘退出时的清理回调
func (a *App) onTrayExit() {}

// shutdown 在窗口关闭时调用
func (a *App) shutdown(ctx context.Context) {
	log.Printf("🔌 正在关闭服务...")
	if a.httpServer != nil {
		_ = a.httpServer.Shutdown(ctx)
	}
	if a.scada != nil {
		a.scada.Close()
	}
	if a.db != nil {
		a.db.Close()
	}
}

// ── 日志 IPC（暴露给前端）───────────────────────────────────────────────────

// GetLogHistory 返回过去 48 小时的历史日志，前端首次加载时调用
func (a *App) GetLogHistory() []logger.Entry {
	return logger.GetHistory()
}

// ── 配置读写 IPC ─────────────────────────────────────────────────────────────

// LoadConfig 读取配置
func (a *App) LoadConfig() (*AppConfig, error) {
	return a.loadConfig()
}

func (a *App) loadConfig() (*AppConfig, error) {
	cfg := &AppConfig{}
	if _, err := toml.DecodeFile(a.configPath, cfg); err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	return cfg, nil
}

// SaveConfig 保存配置
func (a *App) SaveConfig(cfg AppConfig) error {
	f, err := os.Create(a.configPath)
	if err != nil {
		return fmt.Errorf("打开配置文件失败: %w", err)
	}
	defer f.Close()
	header := "# GOKS RestfulAPI 配置文件\n# 此文件与 exe 放在同一目录下，修改后重启生效\n\n"
	if _, err = f.WriteString(header); err != nil {
		return fmt.Errorf("写入注释失败: %w", err)
	}
	return toml.NewEncoder(f).Encode(cfg)
}

// GetConfigPath 返回配置文件路径
func (a *App) GetConfigPath() string {
	return a.configPath
}

// ── SCADA 状态 IPC ───────────────────────────────────────────────────────────

// GetScadaStatus 返回连接状态快照
func (a *App) GetScadaStatus() scada.ConnectionStatus {
	if a.scada == nil {
		return scada.ConnectionStatus{}
	}
	return a.scada.GetConnectionStatus()
}

// RefreshScadaToken 强制刷新 Token
func (a *App) RefreshScadaToken() error {
	if a.scada == nil {
		return fmt.Errorf("SCADA 客户端未初始化")
	}
	return a.scada.Login(true)
}

// ── 数据库状态 IPC ───────────────────────────────────────────────────────────

// GetDBConnectionsList 返回所有数据库连接状态列表，供前端展示与断线重连。
func (a *App) GetDBConnectionsList() []db.ConnStatus {
	if a.db == nil {
		return nil
	}
	return a.db.ListStatus()
}

// ReconnectDB 对指定名称的连接触发断线重连（关闭后按原配置重新建连）。
func (a *App) ReconnectDB(name string) error {
	if a.db == nil {
		return fmt.Errorf("数据库管理器未初始化")
	}
	return a.db.Reconnect(name)
}

// ApplyConfig 保存配置并热重载所有子系统（不重启程序）
func (a *App) ApplyConfig(cfg AppConfig) error {
	// 1. 持久化到磁盘
	if err := a.SaveConfig(cfg); err != nil {
		return err
	}
	// 2. 关闭旧子系统
	if a.httpServer != nil {
		ctx := context.Background()
		_ = a.httpServer.Shutdown(ctx)
		a.httpServer = nil
	}
	if a.scada != nil {
		a.scada.Close()
		a.scada = nil
	}
	if a.db != nil {
		a.db.Close()
		a.db = nil
	}
	// 3. 以新配置重启子系统
	a.startSubsystems(&cfg)
	log.Printf("✅ 配置已热重载")
	// 4. 广播新配置给前端
	runtime.EventsEmit(a.ctx, "config:loaded", &cfg)
	return nil
}

// ── 自启动 IPC ────────────────────────────────────────────────────────────────

const autoStartRegKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autoStartAppName = "GOKS"

// GetAutoStart 返回当前是否已设置开机自启
func (a *App) GetAutoStart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue(autoStartAppName)
	return err == nil && val != ""
}

// SetAutoStart 开启或关闭开机自启
func (a *App) SetAutoStart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表失败: %w", err)
	}
	defer k.Close()

	if enable {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取 exe 路径失败: %w", err)
		}
		if err := k.SetStringValue(autoStartAppName, `"`+exe+`"`); err != nil {
			return fmt.Errorf("写入注册表失败: %w", err)
		}
		log.Printf("✅ 已设置开机自启: %s", exe)
	} else {
		if err := k.DeleteValue(autoStartAppName); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("删除注册表失败: %w", err)
		}
		log.Printf("✅ 已取消开机自启")
	}
	return nil
}

// ── 辅助 ─────────────────────────────────────────────────────────────────────

// findConfigToml 从 startDir 开始向上最多查找 4 级目录，找到 config.toml 就返回。
// 找不到则返回 startDir/config.toml（正式打包后 exe 和 config.toml 在同目录）。
func findConfigToml(startDir string) string {
	dir := startDir
	for i := 0; i < 4; i++ {
		candidate := filepath.Join(dir, "config.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(startDir, "config.toml")
}

func defaultConfig() *AppConfig {
	return &AppConfig{
		MySQL: MysqlConfig{
			Host:     "127.0.0.1",
			Port:     3306,
			DBName:   "sy",
			User:     "root",
			Password: "root",
		},
		SCADA: ScadaConfig{
			BaseURL:  "http://192.168.7.229:9433/api/v1",
			Username: "admin",
			Password: "48D29E9C2707D3966C779A72A7BA06E1",
		},
		API: APIConfig{
			Host: "0.0.0.0",
			Port: 8004,
			MyIP: "127.0.0.1",
		},
	}
}

// dbConnConfigs 从配置生成数据库连接列表：若有 databases 则用，否则用 mysql 生成一条默认连接。
func dbConnConfigs(cfg *AppConfig) []db.ConnConfig {
	if len(cfg.Databases) > 0 {
		return cfg.Databases
	}
	// 兼容旧版单 mysql 配置
	return []db.ConnConfig{{
		Name:     "default",
		Type:     "mysql",
		Host:     cfg.MySQL.Host,
		Port:     cfg.MySQL.Port,
		DBName:   cfg.MySQL.DBName,
		User:     cfg.MySQL.User,
		Password: cfg.MySQL.Password,
	}}
}
