---
description: "Use when: 编写或修改 React 前端代码、调整 UI 组件、处理 Wails IPC 调用、修改样式和日志展示"
applyTo: "frontend/**/*.jsx, frontend/**/*.js, frontend/**/*.css"
---

# 前端规范

## 通信方式

- 前端与 Go 后端之间只走 Wails IPC（`@wailsjs/go/main/App`），严禁 fetch/axios/XMLHttpRequest
- 实时日志通过 `EventsOn("log:entry", ...)` 接收 Wails 事件推送
- 配置加载后通过 `EventsOn("config:loaded", ...)` 广播到前端

## 组件规范

- 只用函数式组件 + React Hooks，禁止 Class 组件
- 当前只有一个主组件 LogWindow.jsx，承载日志展示、配置横幅和设置弹窗
- App.jsx 只做根挂载，不放业务逻辑

## 样式

- 全局黑色主题，背景色 `#0d0d0d`
- 样式用内联 style 对象，不引入 CSS 框架
- 日志行按来源着色（scada 绿、menu 蓝、http 紫、app 橙、db 红）
- 字体优先 Consolas / JetBrains Mono 等等宽字体

## 定位

- 前端是轻量运维界面，不是业务主界面
- 不要把前端扩展成复杂的业务系统
- 功能范围限于：日志查看、配置编辑、连接状态、数据库重连、开机自启
