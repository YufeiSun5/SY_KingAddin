# 数据库并发模型说明

> 创建日期：2026-04-13  
> 最后更新：2026-04-13

## 目的

本文档用于说明当前数据库读写路径是否共用队列、真正的数据库并行数是多少，以及这些行为由哪些代码和配置决定。

## 结论摘要

### 同一连接名下

- batch 写不走普通查询路径，拥有自己的应用层队列
- 普通读/普通直写不走 batch 队列，直接进入连接池
- batch 写和普通读写虽然不是同一个应用层队列，但会共用同一个数据库连接池

### 真正并行数

- 同一连接名下的 batch 写真正执行并行数：`1`
- 同一连接名下的普通读/直写真正数据库并行上限：`30`
- 同一连接名下读写混合时的总数据库并行上限：`30`

说明：

- batch 写是单连接单 worker 模型，不会在同一个连接名下并行开启多个批处理事务
- 普通查询和直写使用 `database/sql` 连接池，并发上限由 `MaxOpenConns` 决定

## 为什么 batch 写并行数是 1

每个连接名只会创建一个 `connBatch`，其中只有一个实时队列：

- 队列定义见 [db/batch.go](db/batch.go#L164)
- 启动时只起一个 `flushLoop` 消费协程，见 [db/batch.go](db/batch.go#L221)

`flushLoop` 的执行模式是：

- 等到一条数据到达
- 在 `flush_interval` 时间窗口内继续收集更多 SQL
- 拼成一批后由一个事务串行执行

关键位置：

- 批次收集窗口在 [db/batch.go](db/batch.go#L704)
- 事务开启在 [db/batch.go](db/batch.go#L831)
- 事务内逐条执行在 [db/batch.go](db/batch.go#L853)

所以同一连接名下，batch 真正执行时只有一条批处理流水线，不是多线程并行写库。

## 为什么普通读写并行上限是 30

普通查询和普通直写不经过 batch 队列，直接走连接池：

- 查询入口见 [db/pool.go](db/pool.go#L242)
- 执行入口见 [db/pool.go](db/pool.go#L356)

连接池上限是硬编码在 [db/pool.go](db/pool.go#L29) 这一组常量里的：

```go
maxOpen     = 30
maxIdle     = 10
connMaxLife = 5 * time.Minute
queryTimeout = 30 * time.Second
```

真正生效的位置在建立底层 `*sql.DB` 时：

- [db/pool.go](db/pool.go#L165)
- [db/pool.go](db/pool.go#L166)

也就是说，同一连接名下最多只会同时打开 30 个数据库连接。第 31 个及之后的请求不会继续并行，只会等待池里空闲连接。

## 为什么读写会互相影响

虽然 batch 写和普通读写不是同一个业务队列，但它们最终都会落到同一个连接名的 `*sql.DB` 上。

例如连接名 `229`：

- batch 写在执行批次时会调用 `pool.DB()` 取到底层连接池，再开启事务，见 [db/batch.go](db/batch.go#L814) 和 [db/batch.go](db/batch.go#L831)
- 普通查询会在同一个连接池上执行 `QueryContext`，见 [db/pool.go](db/pool.go#L252)

因此：

- 它们不共享应用层队列
- 但共享数据库连接池
- 所以会抢连接、抢数据库资源、互相拉高延迟

## 多连接名场景

如果配置了多个连接名，例如：

- `229`
- `odbc`

那么每个连接名有自己独立的：

- `connBatch`
- `queue`
- `*sql.DB` 连接池

因此真实并行能力按“每个连接池各自计算，再相加”理解，而不是整个进程只有一个总队列。

但每个连接池自己的上限仍然受 `maxOpen = 30` 限制。

## 哪些值可以配，哪些值当前不能配

### 当前可配置

batch 相关参数可在 `[batch]` 中配置：

- `queue_size`
- `flush_interval`
- `flush_batch`
- `max_retries`
- `conn_retry_base_interval`
- `conn_retry_max_interval`
- `retry_state_persist_interval`

定义位置：

- [db/config.go](db/config.go#L76)

默认值填充位置：

- [db/config.go](db/config.go#L87)

示例配置位置：

- [config.toml](config.toml#L30)

### 当前不可配置

数据库连接池并发上限目前不是从 `config.toml` 读取的，而是硬编码在：

- [db/pool.go](db/pool.go#L29)

也就是说下面这些值当前需要改代码才能调整：

- `maxOpen`
- `maxIdle`
- `connMaxLife`
- `queryTimeout`

## 对 100 并发的实际含义

### 100 个 batch 入队请求

- 大概率能接住，因为是入队，不要求 100 个数据库事务并行执行
- 同一连接名下最终还是一条批处理执行线在慢慢刷

### 100 个普通查询/直写请求

- 同一连接名下最多 30 个真正并行执行
- 其余请求会在连接池排队等待

### 100 个读写混合请求

- 同一连接名下总数据库并行上限还是 30
- 读和写会相互干扰
- 如果数据库本身慢，等待时间会继续放大

## 建议

如果现场确实需要更高并发，应优先考虑：

- 读写分连接名或分连接池
- 将连接池并发上限改成可配置项
- 对高频查询和 batch 写做连接隔离，而不是都打到 `229`

在未做这些改造前，不建议对“同一连接名下 100 并发读写”做稳定承诺。