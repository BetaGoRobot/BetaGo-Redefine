'use strict';
const messages = {
  MOBILE_GADGET_REQUIRED: '背景音频仅支持飞书 iOS/Android 小程序。请在手机预览，或打开网页。',
  FEISHU_VERSION_UNSUPPORTED: '需要飞书 5.20 或更高版本。',
  BACKGROUND_API_UNAVAILABLE: '当前容器未提供背景音频能力，请检查小程序能力及客户端。',
  SESSION_EXPIRED: '自有服务会话已过期。请重新授权；访客资料需用恢复码取回。',
  GADGET_AUTH_NOT_CONFIGURED: '尚未配置飞书授权码交换服务。',
  LOGIN_FAILED: '飞书授权失败，请重新尝试。',
  INVALID_RECOVERY_CODE: '请输入自有音乐服务生成的完整恢复码。',
  NETWORK_FAILED: '网络请求失败。请检查服务地址、网络及请求域名配置。',
  PROVIDER_UNAVAILABLE: '音源或飞书登录服务暂不可用。',
  SOURCE_UNAVAILABLE: '这首曲目暂无可用音源，不会静默跳过。',
  PREVIEW_ONLY: '音源只提供试听，本原型停止播放，请更换可完整播放的曲目。',
  SOURCE_RETRY_EXHAUSTED: '刷新音源后仍播放失败。已停止自动重试，可手动重试或切歌。',
  SOURCE_ALREADY_EXPIRED: '服务返回的音源已过期，请重试。',
  QUEUE_CONFLICT: '队列已被其他客户端更新。暂停后点击重新连接读取最新版本。',
  EMPTY_QUEUE: '队列为空。请选择自有服务的歌单。',
  WEB_OPEN_FAILED: '网页未能打开，请检查跳转白名单。',
  URL_ORIGIN_DENIED: '音源域名不在配置允许列表内。',
  PLAYBACK_TIMEOUT: '尚未收到播放事件，请检查网络或手动重试。'
};
function message(code) { return messages[code] || ('操作未完成：' + code); }
function clock(value) { const n = Math.floor((value || 0) / 1000); return Math.floor(n / 60) + ':' + ('0' + n % 60).slice(-2); }
module.exports = { message, clock };
