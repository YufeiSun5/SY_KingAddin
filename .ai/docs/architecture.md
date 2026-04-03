# 项目架构详解

> 本文件是 AGENTS.md 的补充参考，按需阅读。

## 核心架构链路

1. Wails 启动桌面进程，加载 `frontend/dist` 作为本地前端界面
2. `app.go` 在启动时读取 `config.toml`，初始化 logger、scada、menu、db 等子系统
3. Go 后端在本机启动 HTTP 服务，默认暴露 `/api/scada`、`/api/menu`、`/api/db` 三组接口
4. React 前端通过 Wails IPC 直接调用 Go 方法，不访问 HTTP 接口
5. 外部系统通过 HTTP 调用本机服务，由插件统一转发到 SCADA 或数据库

前端和后端之间是进程内通信，对外能力才是 HTTP 服务。

## 各模块职责

### app.go — 装配层

- 定位 `config.toml`（开发模式向上最多查找 4 级目录）
- 初始化日志系统
- 启动和热重载 SCADA、数据库、菜单、HTTP 服务
- 向前端暴露 Wails IPC 方法
- 管理托盘菜单和窗口显示/退出行为

### scada/ — SCADA 客户端

- 登录和 Token 生命周期管理（60 分钟有效期，提前 5 分钟刷新）
- 指数退避重试，最大 5 个并发
- 批量读取/写入变量

### db/ — 数据库服务

- 多连接管理（MySQL + ODBC）
- 连接状态查看与断线重连（独立 watchdog，指数退避）
- SQL 执行与错误归类

### menu/ — 菜单管理

- 基于 IP 编号和页码维护 LRU 菜单字典
- 将菜单字符串同步写入 SCADA 变量 `menu`

### logger/ — 日志中枢

- 接管标准 log 输出，解析级别和来源
- 保留内存历史，写入 48 小时 JSONL 文件
- 通过 Wails 事件实时推送给前端

### frontend/ — 运维界面

- 展示实时/历史日志
- 配置编辑并触发热重载
- 连接状态查看、数据库重连、开机自启控制

## 运行方式

- 开发：`wails dev`
- 部署：打包后 exe 与 `config.toml` 同目录运行
- HTTP 服务端口默认 8004

## 配置模型

`config.toml` 包含三部分：

| 配置段 | 内容 |
|--------|------|
| `mysql` / `[[databases]]` | 数据库连接（兼容单源和多源两种写法） |
| `[api]` | HTTP 服务监听地址和对外 IP |
| `[scada]` | SCADA 服务地址与登录凭据 |

前端保存配置后调用 `ApplyConfig`，在不重启程序的情况下重建子系统。
