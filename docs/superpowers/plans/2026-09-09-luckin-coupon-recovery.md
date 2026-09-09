# Luckin coupon failure recovery

**Goal:** 用户确认：优惠券失败后刷新原订单卡，让用户重新选择优惠券并提交。

**Design:** 明确的工具级优惠券拒绝才进入自动刷新；清除已选券、重新预览同一账号及商品、保存新 hash 后展示新报价。刷新失败展示只执行预览的重试按钮。创建请求的网络异常或创建后落库失败属于结果不确定，当前卡不提供再次创建按钮。没有自动 createOrder 重试。

**Scope:** 使用现有 pending 字段，无 schema 变更；添加草稿旧 hash CAS，避免慢预览覆盖更新。此处不实现持久化 submitting/unknown、跨卡账号队列、批次预占或远端 exactly-once；旧回调的持久防重仍需状态机工作包。

## Tasks

- [x] MCP：以 `ToolError` 保留 `isError=true` 的工具响应来源，保留 `errors.Is(ErrRemote)` 兼容性；传输错误不使用此类型。
- [x] 先写服务回归测试：券拒绝后只有一次 create，空选券重新预览，保留 ID/账号/商品/原期限，保存新 hash 与新报价；失败与不确定结果不显示确认按钮。
- [x] 确认服务内完成刷新，错误对象携带恢复卡，动作层继续在原 messageID 更新。刷新失败显示现有改券动作的空选券刷新按钮。
- [x] `UpdateDraft(ctx, order, expectedHash, now)` 在数据库同时匹配旧 hash、pending 和期限；改券入口同步更新，失效请求不覆盖新卡。补充隔离存储谓词和可控交错测试。
- [x] 本地验证刷新按钮、手动刷新失败可重试、原卡 ID、其他子单不受影响；补充使用文档与审查状态。
- [x] 执行受影响测试、差异检查和独立审查。发布使用独立后续 PR（#216 已合并）。

Validation: `GOTOOLCHAIN=local BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.0/bin/go test -v -tags=custom_skip_vips ./internal/application/lark/luckin ./internal/application/lark/luckinaction ./internal/infrastructure/mcpclient ./internal/application/lark/mcpbridge ./internal/application/lark/handlers ./cmd/larkrobot`。仓库层仅执行隔离 fake 测试，不运行真实数据库、真实订单或飞书发送。

## Review and verification

独立审查发现的服务异常误判、延迟错误覆盖新卡均已补充 RED→GREEN 回归并修复。刷新版本包含旧 hash、payload 和报价，避免相同选券下价格变化仍接受旧确认。新增受控并发测试覆盖两张子卡争用同一券，一张成功，另一张只刷新自己的草稿。最终读回与外部 patch 之间仍有时间窗，完整持久化提交与投递状态机不在本次范围。

基于包含 #216 的最新 master 验证：六个应用/客户端/入口包 212 个顶层测试通过，另有 3 个隔离存储测试通过；双子单争券测试的 race 检测通过。未执行真实订单、飞书发送或数据库集成测试。
