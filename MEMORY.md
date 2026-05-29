# 开发进度

> 最后更新：2026-05-29

## 当前阶段

项目处于功能开发阶段。当前重点：批量 SQL 异步执行队列模块（解决 SCADA 大量 INSERT/UPDATE 阻塞问题）。

## 今日已完成

### 文档维护（2026-05-29）

- 为验证 PR 评审流程，新增 `test_html/README.md`，说明本地测试页面目录用途和使用约定。
- 本次为文档级变更，不影响后端接口、前端运维界面或运行时配置。

### 稳定性修复（2026-04-13）

- 修复 `logger/logger.go` 日志时间仅显示时分秒的问题，日志条目改为包含月日，并兼容旧 JSONL 历史解析（影响：logger/logger.go）
- 修复日志窗口首次加载历史时顺序倒置的问题：历史文件改为按“旧 → 新”顺序读入，并按文件日期解析新旧时间格式，避免界面滚到底部后误以为“今天日志没加载”（影响：logger/logger.go）
- 补齐老网页数据库请求兼容：新增 `/api/sql/run-sql` 兼容入口，将旧路径的查询响应恢复为可直接按 `data: [...]` 读取的结构，并在未传 `conn` 时默认走 `229` 连接（无 `229` 配置时回退当前默认连接），兼容旧版菜单页和图表页（影响：db/manager.go / app.go）
- 新增老网页兼容说明文档，梳理图表页/菜单页依赖的旧接口、默认 `229` 查询行为、返回结构兼容点，以及端口/主机写死带来的现场注意事项（影响：.ai/docs/legacy-web-compatibility.md）
- 新增数据库并发模型说明文档，明确同一连接名下 batch 真正并行数为 1、普通读写连接池上限为 30，并标注这些值分别定义在 `db/batch.go`、`db/pool.go`、`db/config.go` 和 `config.toml`（影响：.ai/docs/db-concurrency-model.md）
- 修复 `db/batch.go` 在数据库重连或热重载后仍持有旧连接池导致 `sql: database is closed` 的问题；批量执行改为动态获取最新 Pool，瞬态错误进入重试队列而非死信（影响：db/batch.go）
- 修复 Windows 托盘交互不稳定问题：切换到 `energye/systray`，补上右键显式弹菜单、左键单击显示窗口，并改为独立锁定 OS 线程运行完整消息循环（影响：app.go / go.mod）
- 完成 `db/batch.go` 第一轮两类重试改造：区分普通瞬态重试与断连长期重试，新增重试状态快照文件、启动恢复和空队列自动清理，编译已通过，待运行态回归验证（影响：db/batch.go / db/config.go / config.toml）

### 批量 SQL 执行器（db/batch.go）

- 新增 `db/batch.go`：完整的四级流转批量 SQL 执行器
  - 实时队列：带缓冲 channel（默认 10 万容量），满时阻塞等待
  - 普通重试队列：针对普通瞬态错误做有限指数退避重试，最多 3 次
  - 断连长期重试队列：针对连接断开、连接池关闭等错误做长期指数退避重试
  - 死信队列：内存最近 1000 条 + JSONL 文件按天滚动持久化
  - WAL 预写日志：done-set 精确追踪方案，崩溃后自动恢复未完成 SQL
  - 重试状态快照：普通重试和断连长期重试统一写入连接级快照文件，启动自动恢复，空队列自动删除
  - 每连接独立队列、独立刷出协程、独立日志标签 `[batch:连接名]`
  - 刷出策略：500ms 或 50 条触发，事务批量提交，失败降级逐条执行
  - NOW() 入队时替换为实际时间戳
  - 优雅关闭：排空队列 → 执行剩余 → 数据库不可用时持久化到 WAL
- 修改 `db/config.go`：新增 BatchConfig 结构，支持队列容量/间隔/批次/普通重试次数、断连长期重试退避和重试快照落盘间隔配置化
- 修改 `db/manager.go`：集成 BatchExecutor，关闭顺序优先于连接池
- 修改 `db/server.go`：新增 3 个 HTTP 接口
  - `POST /api/db/batch-exec` — 批量 SQL 入队
  - `GET /api/db/batch-status` — 所有连接队列统计
  - `GET /api/db/batch-dead?conn=xxx` — 死信查询
- 修改 `app.go`：AppConfig 增加 Batch 字段，startSubsystems 中初始化批量执行器
- 修改 `config.toml`：新增 [batch] 配置节
- 修改 `logger/logger.go`：新增 "batch" 日志源识别（优先于 db）

### 磁盘 I/O 性能优化（针对机械硬盘 100% 占用场景）

- WAL 和死信文件缓冲区从 4KB → 64KB，攻满 64KB 才实际写磁盘
- `walMarkDone` 不再每次立即 Flush，改为只写缓冲
- 死信文件改为常驻句柄 + 缓冲写入，不再每条 Open/Write/Close
- 新增 `ioFlushLoop` 协程：每 2 秒统一刷出 WAL + 死信文件缓冲到磁盘
- MySQL DSN 添加 `interpolateParams=true`（省去 Prepare 往返）和 `maxAllowedPacket=16MB`

### 前端日志界面

- 日志来源筛选新增 "BATCH" 标签，与 "DB" 分开展示（查询 vs 批量写入）
- batch 日志用橙色 #ff9800 区分于 db 的红色
- 点击 DB 或 BATCH 后可按连接名二级筛选
- 连接名解析兼容 `[batch:xxx]` 和 `[xxx]` 两种格式

## AI 工程化状态

### 已完成

- AGENTS.md 项目概述（精简版，详情拆至 `.ai/docs/architecture.md`）
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
  - architecture.md（架构详解）、mysql-sync-feature.md（同步功能跟踪）、legacy-web-compatibility.md（老网页兼容说明）、db-concurrency-model.md（数据库并发模型）

## 当前仓库判断

- 项目已具备实际运行痕迹，build/bin 和日志目录中已有历史产物
- 对外 HTTP 接口设计有明显兼容旧版调用方的要求
- 配置热重载、断线重连、Token 生命周期管理是当前系统稳定性的关键部分
- Windows 平台能力是项目的重要组成部分，不只是打包目标

## 后续建议事项

- 持续整理 README、AGENTS.md、MEMORY.md 的职责边界，减少重复维护
- 在目标机实机验证托盘长时间运行后的左右键行为，确认不再出现“开始正常、随后失效”
- 验证 batch 三类错误路径：断连类进入长期重试、普通瞬态错误有限重试、语法/约束错误直接死信
- 验证重试状态快照在断电/重启后的恢复行为，以及空队列自动清理是否符合预期
- 如后续有功能开发，优先记录影响到 SCADA、数据库、菜单同步或外部 HTTP 兼容性的改动
- mysql-sync 模块设计已完成，核心模块待进入开发阶段

## 备注

本文件用于记录阶段性状态、关键判断和近期进展，不替代详细接口文档和设计文档。
