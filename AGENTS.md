# Project Guidelines

## 项目定位

GOKS_restful_API — 运行在 Windows 上的本地插件服务（Wails 桌面宿主 + Go HTTP 服务 + React 运维界面），对外暴露 HTTP 接口供上位机和网页访问，对内统一管理 SCADA 通信、菜单同步和数据库访问。

## 技术栈

- Wails v2 + Go 1.23 + React 18 + Vite
- MySQL / ODBC、Windows 注册表、系统托盘、WebView2

## 模块一览

| 模块 | 职责 |
|------|------|
| `app.go` | 装配层：初始化子系统、暴露 IPC、管理热重载 |
| `scada/` | SCADA 客户端 + Token 管理 + `/api/scada/*` |
| `db/` | 多数据源连接管理 + SQL 执行 + 批量异步执行 + `/api/db/*` |
| `db/batch.go` | 批量 SQL 队列：实时队列 → 重试队列 → 死信队列 + WAL 持久化 |
| `menu/` | LRU 菜单字典 + SCADA 同步 + `/api/menu/*` |
| `logger/` | 日志中枢：内存 + JSONL 文件 + Wails 事件推送 |
| `frontend/` | 轻量运维界面：日志、配置、状态、自启管理 |

详细架构、运行方式和配置模型见 `.ai/docs/architecture.md`。

## 核心约定

- 前后端只走 Wails IPC，不绕本地 HTTP
- 对外 HTTP 接口路径和返回结构不能破坏兼容性
- 最小修改原则，优先保证工业现场稳定性
- 编码规范见 `.ai/instructions/` 目录


# AI 协作路引

> 本文件是 AI Agent 进入本项目的第一份指南。请按顺序阅读以下资料，建立对项目的完整认知后再开始工作。

## 必读文件（按顺序）

1. `AGENTS.md` — 项目定位、架构链路、技术栈、模块职责、维护约定
2. `MEMORY.md` — 开发进度、阶段状态、近期决策记录
3. `.ai/instructions/` 目录下的**所有文件** — 分领域编码规范和工作流约定

## 按需阅读

| 文件 | 何时读 |
|------|--------|
| `.ai/docs/architecture.md` | 需要了解详细架构链路、各模块职责、配置模型时 |
| `.ai/docs/mysql-sync-feature.md` | 涉及数据同步模块开发时 |
| `mysql-sync-design.txt` | 需要数据同步详细设计方案时 |
| `config.toml` | 需要了解运行时配置结构时 |

## 可用技能（.ai/skills/）

技能是可复用的操作模板。当你的任务匹配某个技能的适用场景时，先读取对应的 `SKILL.md` 获取操作步骤。

| 技能 | 适用场景 |
|------|----------|
| `add-api-endpoint` | 在 scada/db/menu 模块中新增 HTTP 接口 |
| `add-wails-ipc` | 新增 Wails IPC 方法供前端调用 |
| `add-module` | 新增独立 Go 模块并接入 app.go |
| `update-progress` | 完成工作后更新 MEMORY.md 和 .ai/docs |

## 文档目录结构

```
.ai-instructions          ← 你正在读的路引（入口）
AGENTS.md                 ← 项目概述（不要频繁修改）
MEMORY.md                 ← 开发进度（每次完成工作后必须更新）
.ai/
  instructions/           ← 分领域编码规范和工作流约定（全部读取）
  docs/                   ← 功能设计与跟踪文档
  skills/                 ← 可复用技能模板
  agents/                 ← 自定义 Agent（职责隔离、工具限制）
  prompts/                ← Prompt 模板（常见任务一键触发）
```

## 可用 Agent（.ai/agents/）

当任务明确属于某个角色时，可委派给对应 Agent：

| Agent | 职责 | 工具限制 |
|-------|------|----------|
| `code-review` | 只读代码审查，检查规范和兼容性 | 仅 read + search |
| `frontend-dev` | 前端界面开发，样式和组件修改 | 不碰 `.go` 文件 |

## 可用 Prompt（.ai/prompts/）

常见任务的快捷入口，自动加载相关规范和技能模板：

| Prompt | 用途 |
|--------|------|
| `new-api` | 新增 HTTP API 接口 |
| `new-ipc` | 新增 Wails IPC 方法 |
| `new-module` | 新增 Go 业务模块 |

## 强制工作流

**开始工作前：**
1. 阅读 MEMORY.md 了解当前进度和上下文
2. 如果任务涉及特定模块，阅读对应的 instructions 和相关源码
3. 如果任务匹配已有技能，读取 SKILL.md 按步骤执行

**完成工作后（必须执行）：**
1. 更新 `MEMORY.md`：记录完成了什么、影响了哪些模块、遗留了什么问题
2. 如果新增了功能模块或接口，更新对应的 `.ai/docs/` 跟踪文件
3. 如果发现了可复用的操作模式，在 `.ai/skills/` 下创建新技能
4. 如果现有 instructions 需要补充（比如新增了编码约定），更新对应文件
5. 不要修改 `AGENTS.md`，除非项目定位或架构发生了根本变化

## 语言要求

- 所有代码注释、日志消息、文档内容使用简体中文
- AI 回复也使用简体中文
