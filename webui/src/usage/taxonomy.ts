export const SCENE_LABELS: Record<string, string> = {
  conversation: '对话生成',
  command: '命令处理',
  routing: '路由决策',
  retrieval: '检索增强',
  agent_runtime: 'Agent 运行',
  evaluation: '效果评测',
  background: '后台处理',
  debug: '调试',
  unknown: '待归类',
}

export const SCENE_COLORS: Record<string, string> = {
  conversation: '#2f7d6d',
  command: '#556fd8',
  routing: '#9a6a30',
  retrieval: '#2e84a6',
  agent_runtime: '#7a58aa',
  evaluation: '#c46155',
  background: '#71807a',
  debug: '#bd7c2a',
  unknown: '#a9b1ad',
}

export const OPERATION_LABELS: Record<string, string> = {
  chat_reply: '普通对话回复',
  mention_reply: '提及回复',
  p2p_reply: '单聊回复',
  command_chat: '命令对话',
  command_handler: '命令处理',
  intent_recognition: '意图识别',
  tool_planning: '工具规划',
  activation: '激活判断',
  relevance: '相关性判断',
  history_search: '历史搜索',
  topic_recall: '话题召回',
  retriever_embedding: '检索向量化',
  retriever_recall: '检索召回',
  retriever_answer: '检索回答',
  callback_continuation: '回调续接',
  candidate_generation: '候选生成',
  judge: '效果裁判',
  message_embedding: '入站消息向量化',
  outbound_embedding: '出站消息向量化',
  chunk_merge: '对话分块合并',
  chunk_embedding: '分块向量化',
  reindex_embedding: '重建索引向量化',
  debug_image: '图片调试',
  debug_conversation: '对话调试',
  unknown: '待归类',
}

export const ATTRIBUTION_LABELS: Record<string, string> = {
  explicit: '业务显式归因',
  legacy_mapping: '历史映射',
  unknown: '待归类',
}

export function sceneLabel(value: string): string {
  return SCENE_LABELS[value] || SCENE_LABELS.unknown
}

export function sceneColor(value: string): string {
  return SCENE_COLORS[value] || SCENE_COLORS.unknown
}

export function operationLabel(value: string): string {
  return OPERATION_LABELS[value] || OPERATION_LABELS.unknown
}

export function attributionLabel(value: string): string {
  return ATTRIBUTION_LABELS[value] || ATTRIBUTION_LABELS.unknown
}
