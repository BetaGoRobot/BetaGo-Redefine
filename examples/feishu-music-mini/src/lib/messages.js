'use strict';
const messages = {
  SEARCH_QUERY_REQUIRED: '请输入歌名后再搜索，最多120个字。',
  INVALID_SEARCH_RESPONSE: '暂时无法读取搜索结果，请稍后重试。',
  QUEUE_LIMIT_REACHED: '播放列表已满，请先选择其他歌单。',
  MOBILE_GADGET_REQUIRED: '请在手机飞书中打开小程序，以使用后台播放；也可打开网页播放器。',
  FEISHU_VERSION_UNSUPPORTED: '需要飞书 5.20 或更高版本。',
  BACKGROUND_API_UNAVAILABLE: '当前环境暂不支持后台播放，请在手机飞书中重试，或打开网页播放器。',
  SESSION_EXPIRED: '登录已过期，请重新连接。访客播放资料可用恢复码找回。',
  GADGET_AUTH_NOT_CONFIGURED: '当前服务尚未开通飞书登录，请联系管理员。',
  LOGIN_FAILED: '飞书授权失败，请重新尝试。',
  INVALID_RECOVERY_CODE: '请输入音乐服务提供的完整恢复码。',
  NETWORK_FAILED: '暂时无法连接音乐服务，请检查网络后重试。',
  PROVIDER_UNAVAILABLE: '音乐或登录服务暂时不可用，请稍后重试。',
  SOURCE_UNAVAILABLE: '这首歌暂时无法播放，请重试或选择其他歌曲。',
  PREVIEW_ONLY: '这首歌只能试听，请选择可完整播放的歌曲。',
  SOURCE_RETRY_EXHAUSTED: '仍然无法播放，已停止自动重试。请点击“重试播放”或选择其他歌曲。',
  SOURCE_ALREADY_EXPIRED: '播放链接已过期，请重试播放。',
  QUEUE_CONFLICT: '歌曲列表已在其他设备更新。请暂停播放，再点击“重新连接并载入歌曲”。',
  EMPTY_QUEUE: '还没有歌曲，请选择歌单。',
  WEB_OPEN_FAILED: '暂时无法打开网页播放器，请稍后重试。',
  URL_ORIGIN_DENIED: '当前服务暂不支持这个播放链接，请联系管理员。',
  PLAYBACK_TIMEOUT: '播放未能开始，请检查网络后重试。'
};
function message(code) { return messages[code] || '操作未完成，请重试。'; }
function clock(value) { const n = Math.floor((value || 0) / 1000); return Math.floor(n / 60) + ':' + ('0' + n % 60).slice(-2); }
module.exports = { message, clock };
