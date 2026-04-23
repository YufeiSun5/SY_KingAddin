---
description: "Use when: 新增或修改 HTTP API 接口、调整响应格式、处理路由注册、编写 server.go"
applyTo: "**/server.go"
---

# HTTP 接口规范

## 响应格式

SCADA 和菜单接口：

```json
{ "code": 0, "data": ... }
{ "code": -1, "message": "错误描述" }
```

数据库接口：

```json
{ "success": true, "data": ... }
{ "success": false, "error": { "errorType": "分类", "message": "详情" } }
```

两套格式是为了兼容 Python 旧版，不要擅自统一。

## 接口路径

- `/api/scada/*` — SCADA 变量读写和状态查询
- `/api/db/*` — SQL 执行和连接状态
- `/api/menu/*` — 菜单 LRU 字典操作和 SCADA 同步

路径命名和参数结构不要随意改动，外部上位机和网页已在调用。

## 兼容性要求

- HTTP 状态码统一 200，不用 4xx/5xx 区分业务错误
- 新增接口可以加，已有接口的路径和返回结构不能破坏
- 数据库旧网页仍可能调用 `/api/sql/run-sql`；在迁移到 `/api/db/run-sql` 之前，必须保留兼容入口，并保证查询结果可直接按旧版 `data: [...]` 方式读取
- 菜单模块的 `/api/menu/unregister/{name}` 同时支持 GET 和 DELETE，兼容 SCADA 上位机的 GET 调用方式
- 菜单模块的 `/api/menu/add/{ip}/{page}` 是简化 GET 入口，保留给上位机用
