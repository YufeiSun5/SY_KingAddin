# 老网页兼容性说明

> 创建日期：2026-04-13  
> 最后更新：2026-04-13

## 目的

本文档用于记录旧版网页对本地插件 HTTP 接口的依赖方式，以及当前 Go 项目为兼容这些旧调用所做的适配，避免后续修改接口时误伤现场页面。

## 覆盖范围

当前已核对的旧页面类型：

- 图表页：通过 `sendSQL(sql)` 请求数据库，直接读取 `result.data[i]`
- 菜单页：通过 `/api/menu/*` 管理菜单，同时通过 `/api/sql/run-sql` 查询权限表

## 兼容结论

### 1. 数据库旧接口

旧页面使用方式：

```javascript
fetch('http://192.168.7.229:8004/api/sql/run-sql', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ sql })
})
```

当前兼容状态：已兼容。

兼容点如下：

- 旧路径 `/api/sql/run-sql` 已重新注册，兼容入口见 [db/manager.go](db/manager.go#L123)
- 旧路径在未传 `conn` 时，会优先使用 `229` 连接；若现场配置中不存在 `229`，则回退到当前默认连接，逻辑见 [db/manager.go](db/manager.go#L176)
- 旧路径的查询返回恢复为旧网页可直接消费的 `data: [...]` 结构，同时保留 `success` 和 `code` 字段，兼容逻辑见 [db/manager.go](db/manager.go#L187)
- 兼容路由在应用启动时统一注册，见 [app.go](app.go#L171)

旧路径返回示例：

```json
{
  "success": true,
  "code": 0,
  "data": [
    { "hour": "01:00", "today_water": 12, "yestoday_water": 8 }
  ],
  "rows": [
    { "hour": "01:00", "today_water": 12, "yestoday_water": 8 }
  ],
  "columns": ["hour", "today_water", "yestoday_water"],
  "count": 1
}
```

说明：

- 旧图表页可继续按 `result.data[i].hour` 方式读取
- 旧菜单页可继续按 `result.success === true || result.code === 0` 判断成功
- 新页面仍可继续使用 `/api/db/run-sql`，两套路径并存

### 2. 菜单接口

旧页面使用方式：

- `POST /api/menu/add`，请求体 `{ ip_code, page_num }`
- `POST /api/menu/remove-by-ip`，请求体 `{ ip_code }`
- `GET /api/menu/list`
- `navigator.sendBeacon('/api/menu/remove-by-ip', json)`

当前兼容状态：已兼容。

兼容依据：

- `POST /api/menu/add` 已支持，见 [menu/server.go](menu/server.go#L31) 和 [menu/server.go](menu/server.go#L60)
- `POST /api/menu/remove-by-ip` 已支持，见 [menu/server.go](menu/server.go#L32) 和 [menu/server.go](menu/server.go#L82)
- `GET /api/menu/list` 已支持，见 [menu/server.go](menu/server.go#L34) 和 [menu/server.go](menu/server.go#L125)
- 旧上位机常见的 GET 简化路径也已保留：`/api/menu/add/{ip}/{page}`、`/api/menu/remove-by-ip/{ip}`、`/api/menu/unregister/{name}`，见 [menu/server.go](menu/server.go#L39)

### 3. 现场仍需注意的非代码问题

以下问题不属于后端接口兼容范围，但会导致“看起来像接口不兼容”：

- 端口不一致：当前打包配置使用 [build/bin/config.toml](build/bin/config.toml#L23) 的 `8005`，但部分旧页面仍写死 `8004`
- 主机地址不一致：旧页面常写死 `192.168.7.229`，而插件对外地址取决于实际部署机器和 [build/bin/config.toml](build/bin/config.toml#L24)
- 页面如果继续写死旧地址，后端即使兼容了旧路径，也无法收到请求

建议：

- 若现场必须“完全不改旧网页”，则插件监听地址和端口必须与旧网页写死值保持一致
- 若允许改网页配置，优先把 API 根地址抽成统一变量，避免后续继续散落写死 IP 和端口

## 已验证页面行为

### 图表页

依赖点：

- 只传 `sql`，不传 `conn`
- 结果直接读取 `data.data.length` 和 `data.data[i].字段名`

兼容结果：

- 已支持，默认落到 `229` 连接

### 菜单页

依赖点：

- 菜单写入调用 `/api/menu/add`
- 权限查询调用 `/api/sql/run-sql`
- 权限查询结果允许两种成功格式：`success === true` 或 `code === 0`

兼容结果：

- 已支持

## 后续约束

后续若修改数据库 HTTP 接口，必须保持以下兼容面不被破坏：

- `/api/sql/run-sql` 旧路径仍可访问
- 旧路径不传 `conn` 时默认落到 `229`
- 旧路径查询结果必须保留 `data: [...]`
- `/api/menu/add`、`/api/menu/remove-by-ip`、`/api/menu/list` 的路径和基本请求结构不能变

如果未来确认现场已全部迁移到新页面，再单独评估是否移除旧兼容入口；在此之前，不应清理这些兼容逻辑。