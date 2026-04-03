---
name: add-module
description: "Use when: 新增 Go 业务模块、创建子系统包（如 sync/alert/export）、扩展新能力域。涉及目录创建、app.go 接入、热重载和 HTTP 路由注册。"
argument-hint: "描述新模块的名称、职责和对外接口"
---

# 添加 Go 模块

## 适用场景

- 项目需要新增独立的功能域（如数据同步、告警通知、数据导出等）
- 新功能的复杂度足以独立成包，不适合塞进现有模块

## 项目约定

### 模块目录结构

每个模块是项目根目录下的独立 Go 包，通常包含：

```
modulename/
  client.go  或  manager.go    ← 核心结构体和业务逻辑
  server.go                     ← HTTP 接口（如需对外暴露）
  config.go                     ← 配置结构体（如有独立配置）
```

参照现有模块：
- `scada/`：client.go（客户端逻辑）+ api.go（API 调用）+ server.go（HTTP 路由）
- `db/`：manager.go（连接管理）+ pool.go（连接池）+ server.go + config.go
- `menu/`：manager.go + server.go

### 接入 app.go 的模式

app.go 中 App 结构体持有各模块实例：

```go
type App struct {
    ctx    context.Context
    // ...现有模块...
    newmod *modulename.Manager  // 新模块实例
}
```

在 `startup()` 或 `domReady()` 中初始化：

```go
a.newmod = modulename.New(cfg, a.logger)
```

如果模块需要暴露 HTTP 接口，在 HTTP 服务启动处注册路由：

```go
a.newmod.RegisterRoutes(mux, "/api/modulename")
```

如果模块需要热重载，在 `ApplyConfig()` 中处理关旧启新。

## 操作步骤

1. **创建模块目录**：`modulename/`
2. **定义核心结构体**：包含模块状态、依赖和配置
3. **实现 New() 构造函数**：接受配置和 logger，返回模块实例
4. **实现业务方法**：核心功能逻辑
5. **如需 HTTP 接口**：添加 server.go，实现 `RegisterRoutes(mux, prefix)`
6. **如需独立配置**：添加 config.go，定义配置结构体
7. **接入 app.go**：在 App 结构体中添加字段，在启动流程中初始化
8. **如需前端交互**：在 app.go 添加 IPC 方法（参考 add-wails-ipc 技能）
9. **更新文档**：
   - 在 `.ai/docs/` 创建功能跟踪文件
   - 更新 MEMORY.md 记录进度

## 关键约束

- 模块包之间禁止循环依赖，跨模块调用通过 app.go 注入回调函数
- 不要引入第三方 HTTP 路由框架，统一用 `net/http` 标准库
- HTTP 接口路径前缀格式：`/api/{modulename}`
- 模块应支持优雅关闭（提供 Stop/Close 方法）
- 配置变更通过 ApplyConfig 热重载，不需要重启进程
- 所有注释和日志使用简体中文
- 如果模块涉及新的外部依赖，先确认 go.mod 中是否需要添加
