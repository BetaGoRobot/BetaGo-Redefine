'use strict';
const { fault } = require('./safe.js');
function versionAtLeast(value, minimum) {
  const a = String(value || '').split('.').map(Number), b = minimum.split('.').map(Number);
  for (let i = 0; i < b.length; i++) { if ((a[i] || 0) !== b[i]) return (a[i] || 0) > b[i]; }
  return true;
}
class FeishuAudio {
  constructor(tt, info, diag) {
    this.tt = tt; this.diag = diag; this.listeners = {};
    const platform = String(info.platform || '').toLowerCase();
    if (platform !== 'android' && platform !== 'ios') throw fault('MOBILE_GADGET_REQUIRED');
    if (!versionAtLeast(info.version, '5.20.0')) throw fault('FEISHU_VERSION_UNSUPPORTED');
    if (typeof tt.getBackgroundAudioManager !== 'function') throw fault('BACKGROUND_API_UNAVAILABLE');
    this.manager = tt.getBackgroundAudioManager();
    ['Canplay', 'Play', 'Pause', 'Stop', 'Ended', 'TimeUpdate', 'Error', 'Waiting', 'Seeking', 'Seeked'].forEach(name => {
      const register = this.manager['on' + name];
      if (typeof register !== 'function') return;
      register.call(this.manager, () => {
        // A delayed pause event from a previous src must not pause the new track.
        if ((name === 'Pause' || name === 'Stop') && this.manager.paused === false) return;
        if (name === 'Play' || name === 'Canplay') this.ready = true;
        if (name === 'Canplay' && this.resumeMs > 0) {
          const position = this.resumeMs; this.resumeMs = 0;
          try { this.manager.seek(position / 1000); }
          catch (_) { if (this.listeners.error) this.listeners.error({}); return; }
        }
        const fn = this.listeners[name.toLowerCase()];
        // Read documented properties. Never pass unsanitized native errors upstream.
        if (fn) fn({ positionMs: this.manager.currentTime * 1000, durationMs: this.manager.duration * 1000, paused: this.manager.paused });
      });
    });
  }
  on(event, fn) { this.listeners[event] = fn; }
  load(source, track, positionMs) {
    const m = this.manager;
    m.title = track.title;
    m.coverImgUrl = source.coverUrl || '';
    m.audioPage = { path: 'pages/player/index', query: {} };
    this.resumeMs = positionMs; this.ready = false;
    m.startTime = positionMs / 1000;
    m.src = source.url;
    // src starts playback in the native manager. Explicit play also handles paused managers.
    m.play();
  }
  play() { this.manager.play(); }
  pause() { this.manager.pause(); }
  stop() { this.manager.stop(); }
  seek(positionMs) {
    if (!this.ready) { this.resumeMs = positionMs; this.manager.startTime = positionMs / 1000; return; }
    this.resumeMs = 0; this.manager.seek(positionMs / 1000);
  }
}
module.exports = { FeishuAudio, versionAtLeast };
