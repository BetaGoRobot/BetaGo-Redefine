# 知识前置方案发布记录

实现源码已通过 gh 推送并核对 490 个文件的 Git blob 摘要；GitHub 提交验证通过。[实现提交](https://github.com/BetaGoRobot/BetaGo-Redefine/commit/7325fe9069aee2b91d12e07c2fc801870bccedb7)与[草稿 PR #266](https://github.com/BetaGoRobot/BetaGo-Redefine/pull/266)可供审阅。

master 和独立 next 的四个应用已依次发布，显式启用 knowledge_first，均健康且重启次数为 0。122 个受保护容器的 ID/image 一致；私有配置摘要一致。两份 Compose 仅修改对应镜像引用和声明的新模式开关。

| 产物 | 版本 |
| --- | --- |
| master | betago-master:knowledge-first-20261030 |
| master image | sha256:2995e6ecfa58f45c6cd1acb3f08cde8e39a38068d9f09c0d9dac0bc24b98f1db |
| next | betago-agent-next:knowledge-first-20261030 |
| next image | sha256:f825a1bebceaea1c16087446b94f7d61e70d70da1a5ac9189e7e5f4ca29431ca |
| 语料代 | cu_3d446725cf0b |
| 知识指纹 | d272c346771c921ff8a77da8fef7b436ce431df8211edbc66a3a0b0e136b6205 |
| 记录 / 知识词条 | 36,836 / 6,683 |

知识 sidecar 已发布并完整回读；原 active 指针未改动，沿用并行维护任务已经激活的语料。两个应用各自保留原有生产依赖及网络、封面、观测修复。回滚镜像分别是 master 的 reaction-observability-20261030-v2 与 next 的 network-fix-20261030；撤回新模式无需回退语料或重放操作。

两个候选版本均以实际只读配置完成初始化、预热和当前来源策略回读；篡改词条指纹与跨 scope 环境被拒绝。现场 AllowedChats 为空，因此未执行现场禁用群分支；当前禁用群的恢复拒绝已由单元回归验证。master 额外保留了一条发布后自然流量的 V2 / 当前语料代 / planner=ready 记录，不保存群 ID、消息 ID 或原话；该记录无词条命中，不能代替幽默质量验收。

实际模型调用累计 11 次 query、12 次 final，保留每次首响应和输入 hash/字节证明。发布后唯一额外 Next 查询发生 8 秒超时，V2 背景“海岛奇兵”仍保留，未重试或记作规划成功。H08 的非法 choice=respond 被原生拒绝；H05/H07 的冻结首响应工具续轮单独通过，原始截断记录未覆盖。没有向群聊发送测试消息。

技术实现、聚焦竞态回归与部署验收完成。已有 6 个共享包陈旧断言、4 个 next 基线失败单独记录，未宣称全仓通过。80 窗口人工幽默评价尚未执行；查询超时、provider 协议错误、部分表达和无关工具选择仍是后续病例优化事项。
