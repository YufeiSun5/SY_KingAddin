---
name: add-wails-ipc
description: "Use when: 新增 Wails IPC 方法、前端调用新的 Go 后端能力、添加运维界面交互入口。涉及 app.go 导出方法和 wailsjs 自动生成。"
argument-hint: "描述要添加的 IPC 方法功能和参数"
---

# 添加 Wails IPC 方法

## 适用场景

- 前端需要调用新的 Go 后端能力（查询状态、触发操作、读写配置等）
- 为运维界面新增交互功能
- 需要把已有内部逻辑暴露给前端

## 项目约定

### 方法定义位置

所有 IPC 方法定义在 `app.go` 的 `App` 结构体上，必须是导出方法（大写开头）：

```go
// 方法功能的中文描述
func (a *App) MethodName(param string) (ReturnType, error) {
    // 业务逻辑委托给对应模块，app.go 不处理具体业务
}
```

### 关键规则

- **app.go 只做装配和转发**，具体业务逻辑在各自模块包中实现
- 方法必须有中文注释
- 复杂返回类型需定义结构体（放在 app.go 或对应模块包中）
- 错误通过 `error` 返回值传递，前端会收到 reject 的 Promise

### 自动生成

运行 `wails dev` 或 `wails generate` 后，以下文件会自动更新：
- `frontend/wailsjs/go/main/App.js` — JS 导出函数
- `frontend/wailsjs/go/main/App.d.ts` — TypeScript 类型定义
- `frontend/wailsjs/go/models.ts` — 参数/返回值的结构体映射

**不要手动编辑这些自动生成的文件。**

### 前端调用方式

```javascript
import { MethodName } from '../../wailsjs/go/main/App.js';
const result = await MethodName('param');
```

所有调用都是 Promise-based，用 async/await 处理。

## 操作步骤

1. **确认功能归属**：判断业务逻辑应在哪个模块中实现（scada/db/menu/logger 或新模块）
2. **在模块包中实现核心逻辑**：如果是新功能，先在对应包中写好方法
3. **在 app.go 中添加导出方法**：调用模块包的方法，做好错误处理
4. **运行 `wails dev`**：触发 wailsjs 自动生成
5. **在 React 组件中导入并调用**：遵循前端规范使用 Hooks + async/await
6. **测试**：确认前端能正确调用并处理返回值和错误

## 关键约束

- 前后端通信严禁使用 HTTP/fetch/axios，只走 Wails IPC
- 方法命名简洁但能表达意图：`GetDBStatus` ✓ `GetDatabaseConnectionStatus` ✗
- 如果返回复杂结构体，确认 wailsjs/go/models.ts 中有正确的类型映射
- 所有注释和日志使用简体中文
