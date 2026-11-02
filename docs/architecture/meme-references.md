# 梗知识召回：master 与 next

范围按 2026-10-29 的最终确认执行：梗 Wiki 作为原始数据源；Milvus 承载关系、情绪链、关键元素和原文解释四路召回；master 与 next 共用实现。候选只作为主模型的重要输入参考。最终回复由现有聊天模型组织，可以改造或不用。没有评测工作台、强制模板成句、独立梗消息发送或在线 Wiki 子串搜索。

## 数据流

离线：Wiki 无关键词目录分页与用途编码并发执行 → 原始详情与来源版本、具体用法及三维投影写 PostgreSQL → 全量校验 → 原文解释及三维投影向量化 → Milvus 写入/回读 → 激活不可变索引代。

在线：原始因果对话 → 一次结构化查询分析（最多一次结构修复）→ 三维向量及 BM25 全文召回、原文解释向量召回 → 三层 RRF 融合去重 → 最多五张用法卡进入主模型 → 主模型选择是否采用并生成最终回复。

Wiki 在线 API 不处理群聊查询，也不负责最终排序。完整原文、来源、用法及投影作为不可变事实保存在 PostgreSQL；Milvus 存检索字段、向量、用法 ID 和完整记录指纹。关系、情绪链、关键元素各走 BM25 与向量；原文解释独立走向量，不混进前三个字段。过长解释按 UTF-8 边界分块，所有分块参与向量均值并做 L2 归一化，策略为 `utf8_32768_mean_l2_v1`。自动推断的条件标为 `pending`，不冒充人工审核或当前群友的事实。

## 召回约束

- 前三维每维最多两条互补查询；空维度不发请求，关键词必须在引用原话中出现。原文解释由程序追加一条原始对话查询，最多七条查询、十三条检索通道，不增加查询分析模型调用。
- 每通道、查询内融合、字段聚合、最终池均最多五个不同 `use_id`，不能在空召回后扩大预算。
- 同一查询先融合向量和全文；字段内对成功查询的名次贡献取平均；再跨字段融合。RRF 常数 60，成功空查询进入分母，失败不进入。
- 使用竞争式并列名次；关系、关键元素、情绪、原文解释分别保留最多 2/1/1/1 个机会，最终仍最多五张卡，不按 ID 拆并列组给予额外保护。
- 保留原始通道命中、分数、排名和异常供诊断。损坏单卡隔离，同 ID 不同 `full` 排除，部分失败明确标为降级。
- 各次运行先固定活动 generation。发布由 PostgreSQL 锁串行化，完成后由已验证发布收据封存；应用发布器对封存代只做回读。Milvus 没有 OpenSearch 的 `index.blocks.write` 开关，不能把应用封存说成服务端物理写保护。

## 主模型输入与飞书输出

master 的单阶段 JSON 和两阶段回复生成器均在生成前调用同一 `memereference.Provider`。next 在原生轮次场景构建时调用一次，循环裁剪预算时不重复调用；启用新路径后不再混入旧字面候选。关闭新功能时保留原有链路。

输入只取可信宿主提供的原始文本、同群且不晚于当前消息的历史；不使用展示名替换、摘要、关键词回复任务或模型自编对话替代原话。next 继续保留这些输入对应的任务来源依赖。

候选列表、分数、检索过程和选择元数据均不能进入群消息。内部 `meme_use_ids` 仅用于日志归因，不是发送正文。master 先解析内部回复并拦截候选说明，再向既有发送流提供自然回复；next 校验原生回复及元数据后进入既有投递账本。无候选、失败或不贴合时继续普通回答或按既有判断旁听。

## 日志

master 使用 `meme reference retrieval`、`meme reference selection` 结构化事件，next 使用 `agent.turn.memes.retrieved`、`agent.turn.memes.attribution` 事件。诊断至少包含：

- 当前消息/请求关联标识、召回状态、降级状态、耗时。
- 真正送给主模型的候选 ID、名称和数量；按字段区分的通道状态。
- 模型报告的已采用 ID，仅认可当次实际输入候选的子集；未采用、缺报、格式错误、越界 ID 分别标注。
- `selection_source=model_reported` 或等价标识，不能把检索分当采用概率，也不能把“已召回”当“已采用”。

新增查询/向量调用不输出原话、凭据或整段卡片正文。不要把日志中的候选数组接到消息发送函数。next 原生运行时其他已有正文诊断开关不属于这次新召回日志的内容。

## 首次导入

详见 [同步命令说明](../../cmd/meme-sync/README.md)。编译、运行和测试仍遵守根目录 AGENTS.md。master 工具链按其 go.mod 使用 1.26.8；next 当前为 1.26.0。

```sh
GOTOOLCHAIN=go1.26.8 /root/.go/go1.26.0/bin/go run -tags=custom_skip_vips \
  ./cmd/meme-sync --config /mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  --dry-run --limit 1
```

dry-run 只读取公开 Wiki，不读取私有配置、不调用模型、不写库。实际同步移除 `--dry-run`；整库导入使用 `--limit 0 --offset 0`，目录结束且全部来源成功后才发布和激活。有限 `--limit` 只用于明确的隔离批次，不满足本次全量启用门。`catalog_complete/has_more/next_offset` 明确显示覆盖范围；成功编码和公开原文缓存可用于补跑失败项。

模型或来源编码失败不会激活残缺的新代；同一来源版本及编码配置可复用缓存。生成/检索向量的模型、维度、索引前缀必须一致。不要把旧代的 embedding 维度改成新值后直接复用。

## master 配置

master 在现有配置增加可选 `[memes]`，默认关闭。省略的数据库连接、查询模型及向量模型从已有配置取值；Milvus 使用独立地址和账户。查询与离线编码默认使用普通模型，也可以显式指定。示例不包含凭据：

```toml
[memes]
enabled = false
schema = "meme_reply"
scope = "wiki"
index_prefix = "memes"
dimensions = 2048
request_timeout_seconds = 12
query_timeout_seconds = 8
start_interval_millis = 100
max_concurrency = 2
allowed_chat_ids = []
query_model = "deepseek-v4-1-flash-economy-261017"
search_endpoint = "http://betago-meme-milvus:19530"
# search_username/search_password 由生产配置安全提供
# insecure_skip_verify = false
```

示例显式选择本次验证可用的模型；需按当前账户选择支持结构化输出的可用模型。开发配置原有的 Mini/Lite 返回已关闭，Pro 在本次编码中输出不完整或超时，未修改这些已有配置。先完成同步并核对活动代，再启用目标范围。`allowed_chat_ids` 为空表示此实例允许的所有群；非空时只在列出的群启用。新模块不创建飞书发送客户端，也不会单独发一条梗消息。

## next 配置与共享语料

next 使用同一 `[memes]` 字段及 `AGENT_NEXT_MEMES_*` 环境覆盖，详见其独立部署示例。它继续使用独立原生 Chat/embedding 适配器，不依赖 master 的 Ark 全局初始化。

若要复用 master 的 Wiki 索引，两个实例需配置同一目录数据库、schema、scope、Milvus 地址、index_prefix、向量模型及维度。next 的 `memes.database_url` 可以指向目录库；不设置时使用 next 原有数据库。目录库只保存公共来源、卡片和发布状态，不需要把群聊消息复制到公共库。

next 的梗向量角色独立于既有记忆向量，避免为这次接梗改动 Hindsight 的维度。原有工具授权、业务回执和群消息投递保持各自职责。

## 验证与交付边界

工程检查覆盖来源分页、严格结构、指纹、真实 PostgreSQL/Milvus 发布与召回、五张预算、错误降级、原生模型请求和最终投递。显式启用的公开 Wiki/真实模型集成不会创建 Lark 客户端，不能替代群友对幽默效果的判断。

早期 OpenSearch 隔离实测仅作为迁移前的历史证据；Milvus 需要独立的发布、回读和完整查询证据，旧镜像和旧索引检查不能证明此次迁移完成。当前部署与实测证据由运行目录记录，并区分完整目录与明确的隔离快照。

本次不创建人工评测入口，也不把真实群作为测试场。生产启用/部署是独立操作；回退只需关闭 `[memes].enabled`，资料及已发布索引代保留。
