# Agent DB Change SOP

适用场景：任何新表、删表、改字段、改索引、改约束，且后续代码需要依赖 PostgreSQL 实际 schema 和 `gorm-gen` 生成结果。

## 强制顺序

1. 先判断这次需求是否涉及数据库 schema 变化。
2. 如果涉及 schema 变化，只做 SQL，不要先写业务代码。
3. 把 SQL 文件保存到 `script/sql/`。
4. 明确通知用户：
   - 需要先执行 SQL
   - 需要再执行 `go run ./cmd/generate`
5. 停在这里，等待用户确认“建表/改表完成，gorm-gen 完成”。
6. 只有在用户确认之后，才能继续：
   - 修改 `cmd/generate` 的特殊字段映射
   - 使用生成后的 `internal/infrastructure/db/model` 和 `query`
   - 编写 repository / service / handler / tests

## 命名要求

- SQL 文件统一放在 `script/sql/`
- 文件名建议：
  - `YYYYMMDD_<topic>.sql`
  - 例如：`20260318_agent_runtime_tables.sql`
- 一个需求对应一个 SQL 文件，文件内同时包含：
  - `create table`
  - `alter table`
  - `create index`
  - `drop index`
  - 必要的 `foreign key` / `unique` / 默认值

## SQL 编写要求

- 默认 schema 使用 `betago`
- 优先写幂等 SQL：
  - `create schema if not exists`
  - `create table if not exists`
  - `create index if not exists`
- 改表语句优先显式表达：
  - `alter table ... add column`
  - `alter table ... alter column`
  - `alter table ... drop column`
- 索引、唯一约束、外键在同一个 SQL 文件里一并补齐
- 不要把 schema 变更只写在对话里，必须落盘到 `script/sql/`

## 明确禁止

- 不要在用户执行 SQL 和 `gorm-gen` 之前，手写最终要被生成模型替代的 GORM model/query
- 不要跳过等待，直接继续写依赖新表的业务代码
- 不要假设用户已经建表
- 不要假设 `gorm-gen` 已经更新

## 标准对用户输出

当 agent 完成 SQL 文件后，应该明确告诉用户：

1. SQL 文件路径
2. 需要执行该 SQL
3. 需要运行 `go run ./cmd/generate`
4. “我会在你完成这两步之后再继续写代码”

推荐话术：

`SQL 已保存到 script/sql/<file>.sql。请先执行该 SQL，再运行 go run ./cmd/generate。完成后告诉我，我再继续改代码。`

## 执行检查清单

- [ ] SQL 已保存到 `script/sql/`
- [ ] SQL 包含本次需求全部 schema 变化
- [ ] 已明确要求用户先执行 SQL
- [ ] 已明确要求用户再执行 `go run ./cmd/generate`
- [ ] 当前未继续编写依赖新 schema 的业务代码

## 当前示例

- `script/sql/20260318_agent_runtime_tables.sql`
