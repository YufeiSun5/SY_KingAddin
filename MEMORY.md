# 开发进度

> 最后更新：2026-04-03

## 当前阶段

项目处于维护优化阶段。当前重点：补齐 AI 协作文档体系、稳定现有桥接能力，为后续维护建立清晰约束和可复用工作流。

## 今日已完成

- 已扫描仓库主结构、入口文件和核心模块关系
- 已补充 AGENTS.md 项目概述，明确项目定位、架构链路、技术栈和维护约定
- 已确认项目核心形态为 Wails 桌面宿主 + Go 本机 HTTP 服务 + React 轻量运维界面
- 已确认后端核心模块边界基本稳定：app、scada、db、menu、logger
- 已完成 AI 协作文档体系搭建（instructions / skills / agents / prompts）
- 已按 Copilot 规范优化：AGENTS.md 精简至 30 行，所有 instructions/skills 加 YAML frontmatter
- 已新增自定义 Agent（code-review、frontend-dev）和 Prompt 模板（new-api、new-ipc、new-module）

## AI 工程化状态

### 已完成

- AGENTS.md 项目概述（精简版，详情拆至 `.ai/docs/architecture.md`）
- .ai-instructions 路引入口（含阅读顺序、文档地图、强制工作流）
- .ai/instructions/ 编码规范（均含 YAML frontmatter）：
  - backend.md（applyTo: `**/*.go`）
  - frontend.md（applyTo: `frontend/**`）
  - http-api.md（applyTo: `**/server.go`）
  - ai-workflow.md（按需加载）
- .ai/skills/ 可复用技能模板：
  - add-api-endpoint/、add-wails-ipc/、add-module/、update-progress/
- .ai/agents/ 自定义 Agent：
  - code-review.agent.md（只读审查）、frontend-dev.agent.md（前端开发）
- .ai/prompts/ Prompt 模板：
  - new-api.prompt.md、new-ipc.prompt.md、new-module.prompt.md
- .ai/docs/ 参考文档：
  - architecture.md（架构详解）、mysql-sync-feature.md（同步功能跟踪）

## 当前仓库判断

- 项目已具备实际运行痕迹，build/bin 和日志目录中已有历史产物
- 对外 HTTP 接口设计有明显兼容旧版调用方的要求
- 配置热重载、断线重连、Token 生命周期管理是当前系统稳定性的关键部分
- Windows 平台能力是项目的重要组成部分，不只是打包目标

## 后续建议事项

- 持续整理 README、AGENTS.md、MEMORY.md 的职责边界，减少重复维护
- 如后续有功能开发，优先记录影响到 SCADA、数据库、菜单同步或外部 HTTP 兼容性的改动
- mysql-sync 模块设计已完成，核心模块待进入开发阶段

## 备注

本文件用于记录阶段性状态、关键判断和近期进展，不替代详细接口文档和设计文档。
