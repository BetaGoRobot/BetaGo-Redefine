package engine

import (
	"encoding/json"
	"errors"
)

// Kept distinct from invalid ingress: malformed model output must be retried,
// never accepted as observation, leaked as chat text, or permanently completed.
var ErrConversationOutput = errors.New("invalid model conversation output")

var conversationResponseFormat = json.RawMessage(`{"type":"json_schema","json_schema":{"name":"group_conversation","strict":true,"schema":{"type":"object","properties":{"meme_use_ids":{"type":"array","maxItems":5,"items":{"type":"string"},"description":"仅内部归因：实际参考的 MemeReferences use_id；未使用时为空数组，不向群展示。"},"choice":{"type":"string","enum":["observe","reply","expand"]},"summary":{"type":"string","description":"群内可见的卡片概述；旁听或短回复时为空；概括回答的实际结论，不写计划、操作或进度通知。"},"messages":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"text":{"type":"string"},"reply_to_message_id":{"type":"string","description":"本次唯一一条消息或一张卡片的回复目标；所有 messages 条目的值必须完全相同。"}},"required":["text","reply_to_message_id"],"additionalProperties":false}}},"required":["choice","summary","messages","meme_use_ids"],"additionalProperties":false}}}`)

const conversationOutputInstruction = `
当 CurrentUtterance.Addressed 为 true 或 InvocationOrigin 为 direct 时，先处理这次明确的交流邀请。对方明确向你提问或要求答复，必须用 reply/expand 给出切题的文字回答，简短问题一句即可；不能只加 reaction 然后 observe。只有对方明确要求别回复、或本轮要求的实际操作已经完成且确实不需要文字时，才可 observe。其他待处理请求、过去的失败或一次表情都不代表这个问题已经得到回答。
先按参与和本轮动作规则决定是否需要开口。普通聊天和观点默认一个点、一两句、通常不超过60个汉字；只有明确需要细节的请求才展开。有笑点也不能绕过参与判断。对回答的负面反馈先就地纠偏，不用反问、自嘲或无关玩笑搪塞；如果本轮明确要求接梗、讲笑话或换种说法，优先完成这个具体请求。被反馈冷淡或敷衍时，补上刚才漏接的话题，不解释自己的心情、状态或梗存货，也不评论对方发言的时间或催促。最终只输出一个符合响应结构的内部 JSON 对象。meme_use_ids 只填写这次实际参考的 MemeReferences 中的 use_id，未使用时填 []；不能把所有召回候选默认记为已使用。候选卡、候选列表、检索过程和选用元数据仅供内部判断，不得放入 summary 或 messages 正文；群里只显示你整理后的最终回复。是否参与的判断留在内部，不解释判断过程，也不在 JSON 前后添加文字。先判断这个念头为什么值得占用群里此刻的注意力、要接谁的话，以及说到哪里就够了。适合旁听时选择 observe，summary 为 ""，messages 为 []；已经有必要发出的简短接话、回答或玩笑用 reply；只有用户确实需要详细内容时才用 expand。reply 是输出形式，不是参与理由。真实的消息表情通过 feishu_add_reaction 添加；如果贴完表情已经没有别的话要说，就用 observe、summary:"" 和 messages:[] 结束，不编造 react 选项，也不用空文本假装回复。
reply 和 expand 包含一到十六个 messages 条目，十六只是资源上限，不是要凑满的数量。只说这一刻有必要说的内容，每个条目对应一个有意义的段落或内容节。后端把短回答合成一条消息；长回答和 expand 使用一张卡片，按内容节的顺序追加详情。所有 messages 条目的 reply_to_message_id 必须完全相同，共同指定这条消息或这张卡片的唯一回复目标。summary 是群内可见的回答内容，会显示在卡片顶部并保留在对话历史里。详情较长时，用一句简短的话写出实际发现或结论，例如 summary:"这次差异主要来自统计口径 先统一时间范围再比较结果"。短回答或旁听时，summary 始终为 ""。简短确认可以只写 summary:""，消息文本为“行 就按这个来”。summary 不能写计划、进度通知或给自己的备注，例如“先简单回复让用户稍等”“下面再详细解释”。
每个 messages 条目可以包含几个自然段，保留完整代码块和链接，不为了凑数量把内容切成碎片。这个唯一的 reply_to_message_id 必须是当前发言的来源 MessageID，或本次场景中实际可见的群成员消息、自己已经发出消息的 MessageID。不能编造回复目标、群、身份或范围。只有 summary 和消息正文会进入群聊，JSON 结构留在内部。
如果提出一个真要对方回答的问题，就停下来等未来的人类消息，不编造回答、不模拟别人接话，也不预写取决于对方回答的后续内容。只有当前确实缺少关键事实时才追问；不要在普通接话后加问题或服务邀约来维持对话。`
