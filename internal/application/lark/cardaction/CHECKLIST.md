# Card Action Checklist

相关文档：
- [设计与架构说明](./README.md)
- [SOP 详细说明](./README.md#新增或修改回调的-sop)

## 新增或修改回调前

- [ ] 已确认这是新增 action，还是复用已有 action
- [ ] 已确认是否需要兼容历史 payload
- [ ] 已确认这是 `sync` 还是 `async`

判断标准：

- `async`
  - [ ] 有明显慢 IO
  - [ ] 需要异步更新卡片
  - [ ] 有超时风险
- `sync`
  - [ ] 只是轻量校验或轻量写操作
  - [ ] 可以直接返回 toast
  - [ ] 可以直接返回刷新后的卡片

## 协议层

- [ ] 在 `pkg/cardaction/action.go` 增加了 action 常量
- [ ] 如有必要，在 `pkg/cardaction/action.go` 增加了字段常量
- [ ] 没有无必要地新增 legacy alias
- [ ] 发卡 payload 使用了 `cardaction.New(...).Payload()`

## 发卡侧

- [ ] 发卡组件已经接入标准 payload
- [ ] 没有在卡片 JSON 里手写散落的 action key
- [ ] 普通按钮参数放在 `value`
- [ ] 输入框即时输入场景已确认走 `input_value`
- [ ] 表单提交场景已确认走 `form_value`

如果是表单：

- [ ] 表单组件 `name` 已定义
- [ ] payload 里的 `form_field` 已定义
- [ ] 解析逻辑里读取的是同一个字段名

## 注册层

- [ ] 在 `internal/application/lark/cardaction/builtin.go` 完成注册
- [ ] 轻量动作使用 `RegisterSync`
- [ ] 重动作使用 `RegisterAsync`

如果是 `async`：

- [ ] handler 返回 `AsyncTask`
- [ ] handler 内没有自己再写 `go`

如果是 `sync`：

- [ ] handler 直接返回 `*callback.CardActionTriggerResponse`
- [ ] 没有塞重 IO

## 业务解析层

- [ ] 回调入参统一通过 `pkg/cardaction.Parse(...)` 进入业务层
- [ ] 没有在业务 handler 里直接手搓飞书原始 JSON 字段
- [ ] 普通点击值通过 `parsed.String/RequiredString(...)` 读取
- [ ] 输入框值通过 `parsed.InputValue` 或统一封装读取
- [ ] 表单值通过 `parsed.FormValue/FormString(...)` 或统一封装读取

## 卡片响应与发送

- [ ] 已区分“发送卡片”和“回调响应”
- [ ] 发送侧允许使用 `card_json`
- [ ] 回调响应里的 `card.type` 只使用 `raw` 或 `template`

如果 callback 要返回卡片：

- [ ] 返回的是 raw/template callback card
- [ ] 即使 payload 是 Card JSON v2，callback response 里也没有使用 `card_json`

## 测试

- [ ] 已补 payload builder 测试
- [ ] 已补解析测试
- [ ] 已补注册模式测试
- [ ] 已补 callback card type 测试

如果改了卡片 JSON：

- [ ] 已确认没有混入不受支持的旧 tag
- [ ] 已确认 schema 与组件层级正确

## 最后自检

- [ ] 旧的高开销回调仍然保持异步
- [ ] 新增的低开销回调才走同步直返
- [ ] 命名没有误导
- [ ] README 已同步更新
