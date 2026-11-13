'use strict';
const { message, clock } = require('../../lib/messages.js');
const { errorCode } = require('../../lib/safe.js');
const { openWeb } = require('../../lib/fallback.js');
const labels = { idle: '等待连接', paused: '已暂停', loading: '正在取源', refreshing: '刷新音源并恢复位置', playing: '播放中', buffering: '缓冲中', error: '播放失败', ended: '队列播放完毕' };
Page({
  data: { mode: '', status: '等待连接', title: 'BetaGo 音乐', queue: [], playlists: [], error: '', saveError: '', connected: false, busy: false, playing: false, unavailable: false, position: 0, duration: 0, time: '0:00', total: '0:00', diagnostics: '', fixture: false },
  onLoad() {
    this.app = getApp();
    this.setData({ mode: this.app.config.mode === 'fixture' ? '测试夹具 · 非真机验证结论' : '自有音乐服务', fixture: this.app.config.mode === 'fixture', hasSession: !!(this.app.api && this.app.api.token), connected: !!this.app.connected });
    if (this.app.startError) { this.setData({ unavailable: true, status: '背景音频不可用', error: message(this.app.startError) }); return; }
    this.unsubscribe = this.app.player.subscribe(s => {
      this.setData({ status: labels[s.status] || s.status, title: s.queue[s.index] ? s.queue[s.index].title : 'BetaGo 音乐',
        queue: s.queue.map((t, i) => ({ id: i, title: t.title, artist: t.artist || '', active: i === s.index })),
        index: s.index, position: s.positionMs / 1000, duration: s.durationMs / 1000, time: clock(s.positionMs), total: clock(s.durationMs),
        playing: s.desiredPlaying, error: s.errorCode ? message(s.errorCode) : '', saveError: s.saveError ? message(s.saveError) : '' });
    });
  },
  onShow() { if (this.app && this.app.player) this.app.player.lifecycle(true); },
  onHide() { if (this.app && this.app.player) this.app.player.lifecycle(false); },
  onUnload() { if (this.unsubscribe) this.unsubscribe(); /* App owns the audio manager. */ },
  async action(fn) {
    if (this.data.busy) return;
    this.setData({ busy: true, error: '' });
    try { await fn(); } catch (e) { this.setData({ error: message(errorCode(e)) }); this.app.diag.add('ui_error', { code: errorCode(e) }); }
    finally { this.setData({ busy: false }); }
  },
  connect() { return this.action(async () => {
    if (this.app.player.state.queue.length) {
      this.app.player.pause(); await this.app.player.saveChain;
    }
    await this.app.connect();
    this.setData({ connected: true, hasSession: true, playlists: await this.app.api.playlists() });
  }); },
  choosePlaylist(e) { return this.action(async () => {
    this.app.player.pause(); await this.app.player.saveChain;
    const session = await this.app.api.usePlaylist(e.currentTarget.dataset.id);
    this.app.player.hydrate(session);
  }); },
  recoveryInput(e) { this.recoveryCode = e.detail.value; },
  recoverSession() { return this.action(async () => {
    this.app.player.pause(); await this.app.player.saveChain;
    await this.app.api.recoverSession(this.recoveryCode);
    this.recoveryCode = ''; this.setData({ recoveryValue: '' }); this.app.player.reset();
    await this.app.connect();
    this.setData({ connected: true, hasSession: true, playlists: await this.app.api.playlists() });
  }); },
  toggle() { if (this.data.playing) this.app.player.pause(); else this.app.player.play(); },
  prev() { this.app.player.next(-1); },
  next() { this.app.player.next(1); },
  chooseTrack(e) { this.app.player.select(Number(e.currentTarget.dataset.index), 0, true); },
  seek(e) { this.app.player.seek(Number(e.detail.value) * 1000); },
  retry() { this.app.player.play(); },
  expire() { return this.action(async () => { await this.app.api.expireFixture(); this.setData({ error: '测试租约已撤销。点击“刷新续播”触发取源恢复；实际 HTTP 403 需等媒体重新请求或 seek。' }); }); },
  refresh() { this.app.player.recover(); },
  async logout() { return this.action(async () => {
    this.app.player.pause(); await this.app.player.saveChain; await this.app.api.logout();
    this.app.player.reset();
    this.app.connected = false;
    this.setData({ connected: false, hasSession: false, playlists: [] });
  }); },
  web() { return this.action(() => openWeb(tt, this.app.config)); },
  exportDiagnostics() {
    const text = this.app.diag.export(this.app.config.mode); this.setData({ diagnostics: text });
    if (typeof tt.setClipboardData === 'function') tt.setClipboardData({ data: text, fail: () => {} });
  }
});
