---
name: add-api-endpoint
description: "Use when: 新增 HTTP 接口、添加路由、扩展 API、编写 handler。适用于 scada/db/menu 模块的 server.go 接口扩展、RegisterRoutes 注册新端点。"
argument-hint: "描述要添加的接口功能和所属模块（scada/db/menu）"
---

# 添加 HTTP API 端点

## 适用场景

- 在 scada/、db/、menu/ 模块中新增 HTTP 接口
- 为现有模块扩展新的查询或操作能力
- 需要对外暴露新的 REST 端点供上位机或网页调用

## 项目约定

### 路由注册

使用 Go 标准库 `net/http.ServeMux`，每个模块通过 `RegisterRoutes(mux, prefix)` 方法注册路由：

```go
func (c *Client) RegisterRoutes(mux *http.ServeMux, prefix string) {
    mux.HandleFunc(prefix+"/your-endpoint", c.handleYourEndpoint)
}
```

路由前缀已在 `app.go` 中固定：
- SCADA → `/api/scada`
- DB → `/api/db`
- Menu → `/api/menu`

**新增端点无需修改 app.go**，只需在模块的 `RegisterRoutes` 中添加一行。

### 处理函数签名

```go
func (c *Client) handleXXX(w http.ResponseWriter, r *http.Request) {
    corsHeaders(w) // 或手动设置 CORS 头
    // 1. 校验 HTTP 方法
    // 2. 解析请求体（POST）或查询参数（GET）
    // 3. 调用业务逻辑
    // 4. 返回 JSON 响应
}
```

### 响应格式（各模块不同，必须遵循所属模块的既有格式）

**SCADA 模块**（scada/server.go）：
```json
// 成功
{"code": 0, "data": ...}
// 失败
{"code": -1, "message": "错误描述"}
```

**DB 模块**（db/server.go）：
```json
// 成功
{"success": true, "data": ...}
// 失败
{"success": false, "error": {"errorType": "...", "message": "..."}}
```

**Menu 模块**（menu/server.go）：直接 JSON 编码返回体。

### CORS 要求

每个处理函数必须设置 CORS 头，OPTIONS 预检请求必须处理：

```go
w.Header().Set("Access-Control-Allow-Origin", "*")
w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
```

## 操作步骤

1. **确认所属模块**：判断新接口属于 scada、db 还是 menu
2. **阅读模块的 server.go**：理解该模块的响应辅助函数（`ok`/`fail`、`dbOK`/`dbFail` 等）
3. **编写 handler 方法**：在模块的 server.go 中添加处理函数，遵循该模块的响应格式
4. **注册路由**：在 `RegisterRoutes` 方法中添加 `mux.HandleFunc(prefix+"/xxx", c.handleXXX)`
5. **验证**：确认代码编译通过，CORS 头已设置，响应格式与模块一致

## 关键约束

- HTTP 状态码统一使用 200，业务状态通过 JSON 字段区分
- 所有注释和错误消息使用简体中文
- 变量命名简洁（`db`, `ctx`, `vals`），避免冗长命名
- `if err != nil` 必须存在且有明确处理
- 不要引入第三方路由库或中间件框架
