---
description: "Use when: 编写或修改 Go 后端代码、添加模块、修改连接和重试逻辑、配置热重载、数据库操作"
applyTo: "**/*.go"
---

# 后端规范

## Go 代码风格

- 变量名简洁：`db`, `ctx`, `cfg`, `mux`，不用 Java 式长命名
- 错误必须显式处理，`if err != nil` 不可省略
- 方法必须有中文注释说明功能
- 禁止使用重量级 ORM，统一用 `database/sql`

## 模块边界

- 业务逻辑只在各自包内（scada/、db/、menu/），不要写进 app.go
- app.go 只做装配和 IPC 暴露，不处理具体业务
- 包之间禁止循环依赖，menu 通过注入函数调用 scada，不直接 import
- 集成 Windows 原生消息循环组件（如托盘、窗口钩子）时，优先使用独立 goroutine + `runtime.LockOSThread()`，避免长期运行后事件失效
- 对数据库重连敏感的后台协程（如 batch worker）不要长期缓存可失效的 `*Pool`，执行前应从 Manager 获取当前连接对象

## HTTP 服务

- 所有对外接口统一走 `net/http` 标准库，不引入第三方路由框架
- 响应格式与 Python 旧版保持一致，HTTP 状态码统一 200，用 `code` 或 `success` 字段区分
- 每组接口由各自包的 `RegisterRoutes` 注册，前缀固定为 `/api/scada`、`/api/db`、`/api/menu`
- CORS 预检必须处理，`Access-Control-Allow-Origin: *`

## 连接与重试

- SCADA Token 有 60 分钟有效期，提前 5 分钟主动刷新
- 数据库每条连接独立 watchdog 断线重连，指数退避
- 并发请求受信号量限制，SCADA 最大 5 个并发
- 修改重试和重连逻辑时必须保守，优先保证工业现场稳定性
- batch 场景下必须区分断连类错误、普通瞬态错误和确定性错误：断连类长期重试，普通瞬态错误有限重试，确定性错误直接死信
- batch 的普通重试和长期重试都要有文件级状态快照；空重试队列时应自动清理快照文件，避免长期残留垃圾文件
- batch 重试恢复顺序必须先恢复 WAL done-set，再恢复重试快照和未完成 WAL，避免同一条 SQL 在重启后被重复执行
- 断连类长期重试的退避和快照落盘频率必须走配置项，不要在代码里散落固定毫秒值

## 配置

- 配置统一从 config.toml 读取，不散落硬编码
- 支持 `ApplyConfig` 热重载：先关旧子系统，再以新配置重启
- 兼容单 `[mysql]` 和多 `[[databases]]` 两种写法
