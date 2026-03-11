# 盛云-王牌插件 · SY-KingAddin

> **一句话定位**：一个以 Wails 桌面窗口为宿主的后台插件，向网页/上位机暴露操作 SCADA 变量和 MySQL 数据库的 HTTP 接口，并在本机日志窗口实时展示所有事件。

---

## 目录

1. [架构总览](#架构总览)
2. [目录结构](#目录结构)
3. [配置文件](#配置文件-configtoml)
4. [HTTP 接口索引](#http-接口索引)
   - [数据库接口](#数据库接口-apidb)
   - [SCADA 接口](#scada-接口-apiscada)
   - [菜单接口](#菜单接口-apimenu)
5. [核心模块说明](#核心模块说明)
6. [设计约束](#设计约束)
7. [快速启动](#快速启动)
8. [打包部署](#打包部署)

---

## 架构总览

```
┌──────────────────────────────────────────────────────────────┐
│                      Wails 桌面进程                            │
│                                                              │
│  ┌────────────┐  IPC/内存绑定  ┌──────────────────────────┐  │
│  │ React 前端  │ ◄──────────► │  app.go  (组装层)         │  │
│  │ 日志+设置  │               └──┬─────────┬──────────┬───┘  │
│  └────────────┘                  │         │          │      │
│                            ┌─────▼──┐ ┌───▼───┐ ┌────▼───┐  │
│                            │ scada  │ │ menu  │ │   db   │  │
│                            │ Token  │ │  LRU  │ │ 连接池  │  │
│                            │ 状态机  │ │ 写入队│ │ watchd │  │
│                            └─────┬──┘ └───┬───┘ └────┬───┘  │
│                                  └────┬───┘          │      │
│                            ┌──────────▼──────────────▼───┐  │
│                            │      HTTP :8004              │  │
│                            │ /api/scada /api/menu /api/db │  │
│                            └─────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  logger: log → 环形缓冲 → JSONL(48h) → Wails 事件    │   │
│  └──────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────┘
        │ HTTP              │ HTTP            │ TCP
        ▼                   ▼                 ▼
  SCADA :9433         网页/Postman       MySQL :3306
  :9433/api/v1
```

**关键约束**：前端与 Go 后端之间**只走 Wails IPC**（内存调用），禁止任何 HTTP/fetch/axios。对外的 HTTP 服务只开放给外部网页和上位机调用。

---

## 目录结构

```
GOKS_restful_API/
├── main.go              # 程序入口，Wails 配置（无边框、隐藏关闭、窗口大小）
├── app.go               # App 结构体：组装所有模块，暴露 IPC 方法给前端
├── wails.json           # Wails 项目配置（名称、版本、图标等）
├── config.toml          # 运行时配置（与 exe 同目录）
│
├── db/                  # MySQL 数据库包（移植自 Python query_sql.py）
│   ├── pool.go          # Pool 结构体、连接池、指数退避无限重连 watchdog
│   └── server.go        # HTTP 路由层：把 SQL 执行能力暴露为 /api/db/* 接口
│
├── scada/               # SCADA HTTP 客户端包
│   ├── client.go        # Client 结构体、Token 状态机、指数退避、连接池
│   ├── api.go           # 业务方法：Login / GetVariables / ReadVariables / WriteVariables
│   └── server.go        # HTTP 路由层：把 scada 能力暴露为 /api/scada/* 接口
│
├── menu/                # 菜单管理器包
│   ├── manager.go       # LRU 内存字典 + 智能写入队列 + SCADA 同步
│   └── server.go        # HTTP 路由层：把菜单操作暴露为 /api/menu/* 接口
│
├── logger/              # 日志拦截与推送包
│   └── logger.go        # 接管 log 输出 → 解析级别/来源 → 持久化 + 推送前端
│
├── frontend/            # React 前端
│   └── src/
│       ├── App.jsx      # 根组件
│       ├── App.css      # 全局黑色主题
│       └── LogWindow.jsx # 日志窗口 + 配置横幅 + 设置面板
│
├── build/               # Wails 构建产物
│   ├── logo.png         # 原始 Logo（用于生成图标）
│   └── windows/
│       └── icon.ico     # 窗口/托盘/任务栏图标（由 logo.png 生成）
│
└── go.mod               # Go 模块定义（模块名: temp_init）
```

---

## 配置文件 `config.toml`

放在**与 exe 相同目录**下，支持在设置面板中**热重载**（无需重启程序）。

**单连接（兼容旧版）**：仅配置 `[mysql]` 时，使用一条名为 `default` 的 MySQL 连接。

**多连接与 ODBC**：配置 `[[databases]]` 时可同时使用多条连接，每条可起名、支持 MySQL 或 ODBC（如 DSN 数据源）。

```toml
# 方式一：单连接（仅 [mysql] 时生效）
[mysql]
host     = "127.0.0.1"
port     = 3306
dbname   = "sy"
user     = "root"
password = "123456"

# 方式二：多连接（存在时优先于 [mysql]）
[[databases]]
name     = "主库"
type     = "mysql"
host     = "127.0.0.1"
port     = 3306
dbname   = "sy"
user     = "root"
password = "123456"

[[databases]]
name     = "SCADA"
type     = "odbc"
dsn      = "mtznh"
user     = "root"
password = "root"

[api]
host  = "0.0.0.0"
port  = 8004
my_ip = "192.168.7.253"

[scada]
base_url = "http://192.168.7.229:9433/api/v1"
username = "admin"
password = "48D29E9C2707D3966C779A72A7BA06E1"
```

> **开发模式**：`wails dev` 编译产物在 `build/bin/`，程序会**向上最多查找 4 级目录**自动定位 `config.toml`，无需手动复制。

---

## HTTP 接口索引

所有接口统一返回 `{"code": 0, "data": ...}` 或 `{"code": -1, "message": "..."}` 格式，HTTP 状态码统一为 200（与 Python 版保持一致）。

### 数据库接口 `/api/db`

支持**多数据源**（MySQL + ODBC）、按名称指定连接，每条连接独立 **watchdog 断线重连**。

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/db/run-sql` | 执行 SQL，请求体可带 `"conn": "连接名"` 指定数据源，缺省用默认连接 |
| `GET` | `/api/db/status` | 返回所有连接的状态列表（名称、类型、是否已连接、可读描述） |

#### `POST /api/db/run-sql` 请求/响应

```json
// 请求体（可选 conn 指定数据源）
{"sql": "SELECT * FROM sy LIMIT 10"}
{"sql": "SELECT 1", "conn": "SCADA"}

// 成功响应（SELECT 类型）
{
  "success": true,
  "data": {
    "columns": ["id", "name", "value"],
    "rows": [{"id": 1, "name": "foo", "value": "bar"}],
    "count": 1
  }
}

// 请求体（修改语句）
{"sql": "UPDATE t SET name='test' WHERE id=1"}

// 成功响应（INSERT/UPDATE/DELETE 类型）
{
  "success": true,
  "data": {
    "affected_rows": 1,
    "last_insert_id": 0
  }
}

// 失败响应（统一 HTTP 200，通过 success 字段区分）
{
  "success": false,
  "error": {
    "errorType": "SQL语法错误",
    "message": "..."
  }
}
```

**支持的查询前缀**（自动识别为 SELECT 类型）：`SELECT`、`WITH`、`SHOW`、`DESCRIBE`、`EXPLAIN`

**错误类型（errorType）**：

| errorType | 原因 |
|-----------|------|
| `数据库未连接` | watchdog 正在重连，稍后重试 |
| `SQL语法错误` | SQL 书写错误 |
| `数据库运行错误` | 表/列不存在、权限不足 |
| `数据库连接错误` | 网络中断、超时 |
| `SQL执行错误` | 其他执行异常 |

#### `GET /api/db/status` 响应示例

```json
{
  "success": true,
  "data": {
    "connections": [
      {
        "name": "主库",
        "type": "mysql",
        "is_connected": true,
        "display": "127.0.0.1:3306/sy"
      },
      {
        "name": "SCADA",
        "type": "odbc",
        "is_connected": true,
        "display": "odbc:mtznh"
      }
    ]
  }
}
```

---

### SCADA 接口 `/api/scada`

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/scada/status` | 连接状态快照（Token 剩余时间、是否连接等），不发出 SCADA 请求 |
| `POST` | `/api/scada/refresh-token` | 强制刷新 Token：先删旧 Token，再重新登录 |
| `GET` | `/api/scada/variables` | 从 SCADA 拉取全部变量列表（约 4000+ 条） |
| `POST` | `/api/scada/read` | 批量读取变量实时值 |
| `POST` | `/api/scada/write` | 批量写入变量值 |

#### `GET /api/scada/status` 响应示例

```json
{
  "code": 0,
  "data": {
    "is_connected": true,
    "has_token": true,
    "token_preview": "034cad86...",
    "token_acquired_at": "2026-03-10 09:34:35",
    "token_expires_at": "2026-03-10 10:34:35",
    "token_refresh_at": "2026-03-10 10:29:35",
    "cooldown_seconds": 60,
    "reconnect_available": true,
    "cooldown_remaining": 0
  }
}
```

#### `POST /api/scada/read` 请求/响应

```json
// 请求体
{"tags": ["E220002_B项电压", "menu"]}

// 响应
{
  "code": 0,
  "data": [
    {"N": "E220002_B项电压", "V": "220.5", "Q": 192, "T": "2026-03-10 09:17:13:864"},
    {"N": "menu", "V": "242_60201@274_60303", "Q": 192, "T": "2026-03-10 09:17:13:864"}
  ]
}
```

> 质量码 `Q=192` 表示数据正常；`Q=0` 表示数据无效。

#### `POST /api/scada/write` 请求/响应

```json
// 请求体
{"items": [{"N": "点击二层选项缓存", "V": "Group2_4"}]}

// 响应
{"code": 0, "message": "写入成功", "count": 1}
```

---

### 菜单接口 `/api/menu`

菜单管理器维护一张 **LRU 字典**（最多 10 项），每次变更后自动写入 SCADA 的 `menu` 变量。

`menu` 变量格式：`IP标识_页码@IP标识_页码`，如 `242_60201@274_60303`，总长度不超过 128 字符（SCADA 字符串限制）。

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/menu/list` | 获取当前菜单列表 + SCADA 字符串 |
| `POST` | `/api/menu/add` | 添加/更新菜单项（同 IP 只保留一条，自动 LRU 淘汰） |
| `POST` | `/api/menu/remove-by-ip` | 按 IP 标识删除 |
| `POST` | `/api/menu/remove-exact` | 按 IP + 页码精确删除 |
| `POST` | `/api/menu/clear` | 清空所有菜单项 |
| `POST` | `/api/menu/set` | 整体替换菜单字典 |
| `GET` | `/api/menu/add/{ip}/{page}` | 添加菜单项（GET 简化版，供 SCADA 上位机调用） |
| `GET` | `/api/menu/remove-by-ip/{ip}` | 按 IP 删除（GET 简化版） |
| `GET` | `/api/menu/unregister/{name}` | 注销用户（兼容 SCADA `RequestJsonInfo`） |
| `DELETE` | `/api/menu/unregister/{name}` | 注销用户（RESTful 规范版） |

#### `POST /api/menu/add` 请求/响应

```json
// 请求体
{"ip_code": "设备A", "page_num": 78}

// 响应
{
  "code": 0,
  "action": "created",         // created | updated | refreshed
  "menu_dict": {"设备A": 78},
  "menu_string": "设备A_78",
  "current_count": 1,
  "max_count": 10,
  "sync_result": {"code": 0, "message": "success"}
}
```

#### SCADA 上位机调用示例（`RequestJsonInfo`）

```
// 添加菜单项
RequestJsonInfo("http://192.168.7.253:8004/api/menu/add/设备A/78", 0, "", "", "result");

// 注销用户
RequestJsonInfo("http://192.168.7.253:8004/api/menu/unregister/张三", 0, "", "", "result");
```

---

## 核心模块说明

### `db` 包 — 多数据源连接（MySQL + ODBC）

- **多连接**：`Manager` 管理多条命名连接，每条一个 `Pool`，配置见 `config.toml` 的 `[[databases]]`。
- **类型**：`type=mysql` 使用 go-sql-driver/mysql；`type=odbc` 使用 ODBC 驱动（如 DSN 名称）。
- **连接池参数**（每条连接）：MaxOpenConns 30、MaxIdleConns 10、ConnMaxLifetime 5 分钟、单次查询超时 30 秒。

**断线无限重连（每条连接独立 watchdog）**

```
启动 → 尝试连接 → 失败 → 等 1s → 重试 → 失败 → 等 2s → ... → 上限 32s
                → 成功 → 定期 ping（每 15s）→ ping 失败 → 重置退避 → 重连
```

前端可调用 **断线重连**：对指定连接名触发一次关闭并按原配置重新建连（watchdog 会继续保活）。

**SQL 类型自动判断**

- `SELECT / WITH / SHOW / DESCRIBE / EXPLAIN` → 返回行数据
- `INSERT / UPDATE / DELETE / DDL` → 返回受影响行数

---

### `scada` 包 — SCADA HTTP 客户端

**Token 管理（双轨机制）**

| 触发方式 | 说明 |
|---------|------|
| 主动（Ticker） | 每 50 分钟定时触发 `Login(false)` |
| 被动（业务层） | API 返回 `code=-5`（Token 失效）时，`isCritical` 回调立即触发 `Login(true)` |

**指数退避重试**

```
失败 → 等 1s → 重试 → 失败 → 等 2s → 重试 → ... → 上限 32s
```

三个业务方法（`GetVariables` / `ReadVariables` / `WriteVariables`）全部通过 `retryWithBackoff` 包裹，调用方无需处理重试。

**线程安全**

- `sync.RWMutex` 保护 Token 字段读写
- `atomic.Bool` 防止并发触发 Login
- `sem`（容量 5 的 channel）限制同时最多 5 个 HTTP 请求

**关闭时释放 Token**

`Close()` 的执行顺序：
1. 读出当前 Token
2. 用独立 5s 超时的 context 发 `POST /DelToken`（不受主 ctx 影响）
3. 清空本地 Token 记录
4. `cancel()` 停止后台协程

---

### `menu` 包 — LRU 菜单管理器

**LRU 策略**

使用 `container/list` 双向链表 + `map` 实现 O(1) 的 LRU：
- 最多保留 10 个菜单项
- 新增时若已满，自动淘汰最久未访问的项
- 访问/更新已有项时，移动到链表尾部（最近使用）

**智能写入队列**

```
并发写入时：
  - 当前无写入 → 立即写入
  - 当前正在写 → 排队（新值替换旧的排队值，只保留最新）
  - 上一次写完 → 检查排队值，有则继续写
```

保证顺序写入，同时丢弃中间值，只同步最终状态到 SCADA。

**`menu` 变量格式**

```
排序规则：纯数字 IP 优先（按数值升序），字符串 IP 按字典序
示例：242_60201@274_60303@设备A_78
```

---

### `logger` 包 — 日志拦截与推送

**接管标准库 `log`**

通过 `log.SetOutput(&logWriter{})` 拦截所有 `log.Printf` 输出，解析后分发。

**来源识别规则**（按关键词匹配）

| 来源标签 | 关键词 |
|---------|--------|
| `scada` | scada, token, gettoken, deltoken, tcp |
| `menu` | menu, lru, 菜单 |
| `http` | http, /api/, listen |
| `app` | startup, shutdown, config, 配置 |

**持久化**

- 文件路径：`exe同目录/logs/goks-YYYY-MM-DD.jsonl`
- 每行一条 JSON，格式：`{"time":"15:04:05.000","level":"info","source":"scada","message":"..."}`
- 启动时自动加载过去 48 小时历史到内存
- 每天午夜自动滚动新文件，超过 48 小时的旧文件自动删除

---

### `app.go` — 组装层（IPC 方法索引）

所有暴露给 React 前端的 IPC 方法定义在此，通过 Wails 自动生成绑定。

| 方法 | 说明 |
|------|------|
| `GetLogHistory() []Entry` | 返回过去 48h 历史日志，前端首次加载时调用 |
| `GetScadaStatus() ConnectionStatus` | 返回 SCADA 连接状态快照 |
| `RefreshScadaToken() error` | 强制刷新 SCADA Token |
| `GetDBConnectionsList() []ConnStatus` | 返回所有数据库连接状态（名称、类型、是否已连接、可读描述） |
| `ReconnectDB(name string) error` | 对指定连接触发断线重连 |
| `LoadConfig() (*AppConfig, error)` | 读取 config.toml |
| `SaveConfig(AppConfig) error` | 写入 config.toml（仅持久化，不重载服务） |
| `ApplyConfig(AppConfig) error` | 保存配置并**热重载**所有子系统（无需重启） |
| `GetConfigPath() string` | 返回配置文件绝对路径 |
| `GetAutoStart() bool` | 读取开机自启状态（注册表） |
| `SetAutoStart(bool) error` | 设置/取消开机自启（写注册表） |

**实时事件推送**（`EventsOn`）

| 事件名 | 数据类型 | 说明 |
|--------|---------|------|
| `log:entry` | `Entry` | 每条新日志实时推送 |
| `config:loaded` | `AppConfig` | 启动成功或热重载后推送最新配置 |

---

### 前端功能

**配置信息横幅**（标题栏下方）

启动后自动展示当前生效的配置摘要：数据库连接（多连接时展示各连接名称与描述）、API 监听地址、SCADA 接口地址、配置文件路径。热重载后自动刷新。

**状态栏**

- SCADA 连接状态、**数据库连接列表**（每条显示名称与状态点，可点击 **重连** 触发断线重连）
- 日志条数、跟随/清空、设置按钮

**设置面板**（状态栏 ⚙ 按钮）

- **当前数据库连接**（只读列表）；多连接与 ODBC 需在 `config.toml` 中配置 `[[databases]]`
- MySQL（默认连接）/ API / SCADA 三个分区，支持全字段在线编辑
- **开机自启开关**：拨动式 Toggle，直接读写 Windows 注册表
- **保存并热重载**：一键保存配置 + 原地重启所有后端服务，无需关闭程序

---

## 设计约束

1. **Zero-Network Policy**：前后端交互**只走 Wails IPC**（`@wailsjs/go` 内存绑定），前端代码中禁止出现任何 URL 或端口号
2. **数据库驱动**：使用 `database/sql` + go-sql-driver，禁止 ORM
3. **KISS 原则**：禁止过度封装，每个包只做一件事
4. **错误优先**：所有 Go 方法必须包含 `if err != nil` 检查并有明确返回
5. **无状态设计**：业务方法尽量保持无状态，`scada.Client` 的状态通过锁保护

---

## 快速启动

```powershell
# 开发模式（热重载）
cd GOKS_restful_API
wails dev

# 窗口启动后，在 Postman 测试
GET  http://127.0.0.1:8004/api/scada/status
POST http://127.0.0.1:8004/api/scada/read   body: {"tags":["menu"]}
```

---

## 打包部署

```powershell
cd GOKS_restful_API
wails build
```

产物：`build/bin/SY-KingAddin.exe`

部署时将以下文件放在**同一目录**：

```
部署目录/
├── SY-KingAddin.exe  # 主程序（盛云-王牌插件）
├── config.toml        # 配置文件（必须与 exe 同目录）
└── logs/              # 自动创建，存放 48h 日志
```

**启动行为**：
- 程序启动后自动最小化到系统托盘
- 点击托盘图标显示日志窗口
- 右键托盘图标 → "退出 GOKS" 才会彻底退出（同时释放 SCADA Token）
- 点击窗口 ✕ 只隐藏窗口，服务继续在后台运行
- 设置面板支持在线修改配置并热重载，无需重启

构建
32 位构建
$env:GOARCH = "386"
wails build
64 位构建
$env:GOARCH = "amd64"
wails build