'use strict';
const { message, clock } = require('../../lib/messages.js');
const { errorCode } = require('../../lib/safe.js');
const { openWeb } = require('../../lib/fallback.js');
const labels = { idle: '等待连接', paused: '已暂停', loading: '正在准备播放', refreshing: '正在恢复播放', playing: '播放中', buffering: '缓冲中', error: '播放失败', ended: '播放列表已播完' };
Page({
  data: { mode: '', status: '等待连接', title: 'BetaGo 音乐', queue: [], playlists: [], searchResults: [], searched: false, error: '', saveError: '', connected: false, busy: false, playing: false, unavailable: false, position: 0, duration: 0, time: '0:00', total: '0:00', diagnostics: '', fixture: false },
  onLoad() {
    this.app = getApp();
    this.setData({ mode: this.app.config.mode === 'fixture' ? '测试音频 · 仍需在手机上验证' : '自有音乐服务', fixture: this.app.config.mode === 'fixture', hasSession: !!(this.app.api && this.app.api.token), connected: !!this.app.connected });
    if (this.app.startError) { this.setData({ unavailable: true, status: '后台播放不可用', error: message(this.app.startError) }); return; }
    this.unsubscribe = this.app.player.subscribe(s => {
      this.setData({ status: labels[s.status] || s.status, title: s.queue[s.index] ? s.queue[s.index].title : 'BetaGo 音乐',
        queue: s.queue.map((t, i) => ({ id: i, title: t.title, artist: t.artist || '', active: i === s.index })),
        index: s.index, position: s.positionMs / 1000, duration: s.durationMs / 1000, time: clock(s.positionMs), total: clock(s.durationMs),
        playing: s.desiredPlaying, queueChanging: s.queueChanging, error: s.errorCode ? message(s.errorCode) : '', saveError: s.saveError ? message(s.saveError) : '' });
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
    await this.app.player.replaceQueue(() => {
      // A reload may install a newer remote revision before metadata is ready.
      // A failed reload must not leave the old IDs writable at that revision.
      this.app.connected = false;
      this.app.player.reset();
      this.setData({ connected: false, hasSession: !!this.app.api.token, playlists: [], searchResults: [], searched: false });
      return this.app.connect();
    });
    this.setData({ connected: true, hasSession: true, playlists: await this.app.api.playlists() });
  }); },
  choosePlaylist(e) { return this.action(async () => {
    await this.app.player.replaceQueue(() => this.app.api.usePlaylist(e.currentTarget.dataset.id));
  }); },
  searchInput(e) { this.searchQuery = e.detail.value; },
  searchTracks() { return this.action(async () => {
    const tracks = await this.app.api.search(this.searchQuery);
    this.setData({ searchResults: tracks, searched: true });
  }); },
  playSearchResult(e) { return this.action(async () => {
    const track = (this.data.searchResults || [])[Number(e.currentTarget.dataset.index)];
    if (!track) return;
    const queue = this.app.player.state.queue.slice();
    let index = queue.findIndex(t => t.id === track.id);
    if (index < 0) { index = queue.length; queue.push(track); }
    await this.app.player.replaceQueue(() => this.app.api.useTracks(queue, index));
    await this.app.player.play();
  }); },
  recoveryInput(e) { this.recoveryCode = e.detail.value; },
  recoverSession() { return this.action(async () => {
    await this.app.player.replaceQueue(async () => {
      await this.app.api.recoverSession(this.recoveryCode);
      // The token now belongs to the recovered account. Never retain the prior
      // account's queue if loading this account's metadata fails.
      this.app.connected = false;
      this.app.player.reset();
      this.setData({ connected: false, hasSession: true, playlists: [], searchResults: [], searched: false });
      this.recoveryCode = ''; this.setData({ recoveryValue: '' });
      return this.app.connect();
    });
    this.setData({ connected: true, hasSession: true, playlists: await this.app.api.playlists() });
  }); },
  toggle() { if (this.data.playing) this.app.player.pause(); else this.app.player.play(); },
  prev() { this.app.player.next(-1); },
  next() { this.app.player.next(1); },
  chooseTrack(e) { this.app.player.select(Number(e.currentTarget.dataset.index), 0, true); },
  seek(e) { this.app.player.seek(Number(e.detail.value) * 1000); },
  retry() { this.app.player.play(); },
  expire() { return this.action(async () => { await this.app.api.expireFixture(); this.setData({ error: '测试音频的旧播放链接已设为过期。点击“刷新续播”可测试恢复播放；已缓冲的部分可能仍会继续播放。' }); }); },
  refresh() { this.app.player.recover(); },
  async logout() { return this.action(async () => {
    await this.app.player.replaceQueue(async () => { await this.app.api.logout(); return { queue: [] }; });
    this.app.connected = false;
    this.setData({ connected: false, hasSession: false, playlists: [], searchResults: [], searched: false });
  }); },
  web() { return this.action(() => openWeb(tt, this.app.config)); },
  exportDiagnostics() {
    const text = this.app.diag.export(this.app.config.mode); this.setData({ diagnostics: text });
    if (typeof tt.setClipboardData === 'function') tt.setClipboardData({ data: text, fail: () => {} });
  }
});
