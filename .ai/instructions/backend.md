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

## 配置

- 配置统一从 config.toml 读取，不散落硬编码
- 支持 `ApplyConfig` 热重载：先关旧子系统，再以新配置重启
- 兼容单 `[mysql]` 和多 `[[databases]]` 两种写法
