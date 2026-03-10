# 权限点与 Scope 约束

## 当前约定

仓库内所有带 `scope` 概念的写操作，默认都不再走“硬编码超管”模式，而是统一走“权限点 + scope”约束。

当前已经落地的首条规则：

- 修改 `global` scope 配置时，操作人必须持有 `config.write@global`
- 授权数据存放在 `permission_grants`
- 当前授权主体先只支持 `subject_type = user`

## 为什么要这样约束

- `global/chat/user` 本身就是作用域边界，权限模型应该直接落在这个边界上
- 后续配置项、功能开关、任务能力如果继续增长，单独维护“超管名单”会越来越不可控
- 权限点可以按能力拆分，scope 可以按影响范围拆分，后续扩展成本更低

## 后续新增能力的要求

只要新功能满足下面任一条件，就必须先引入 scope 约束，再允许上线：

- 会写入 `global/chat/user/...` 任一 scope 下的数据
- 会修改某类配置项、功能开关、任务策略、治理策略
- 会对群、用户、群内用户这类资源产生管理动作

落地时至少要明确四件事：

1. 权限点名称，例如 `config.write`、`feature.write`、`schedule.manage`
2. scope 语义，例如 `global`、`chat`、`user`、`chat_user`
3. 资源维度是否需要 `resource_chat_id`、`resource_user_id`
4. 入口校验点放在哪里，不能只在 UI 或文档层约束

## 明确禁止

- 先实现 `global` scope 写能力，后补权限校验
- 用新的“特殊管理员表”绕开权限点模型
- 只校验前端/卡片展示，不校验命令、回调或服务入口

## 备注

如果后续新增配置项，或者实现任何和 `scope` 强相关的功能，默认都要同步补：

- 对应的权限点定义
- `permission_grants` 授权策略
- 入口鉴权代码
- README / 设计文档中的约束说明
