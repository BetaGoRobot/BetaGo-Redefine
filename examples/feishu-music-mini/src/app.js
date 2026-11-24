'use strict';
const config = require('./config.js');
const { Diagnostics } = require('./lib/diagnostics.js');
const { FeishuAudio } = require('./lib/background-audio.js');
const { MusicApi } = require('./lib/music-api.js');
const { Player } = require('./lib/player.js');
const { storage } = require('./lib/storage.js');
const { errorCode } = require('./lib/safe.js');
App({
  config,
  onLaunch() {
    this.diag = new Diagnostics();
    let info = {};
    try { info = tt.getSystemInfoSync(); } catch (_) { /* clear unavailable state below */ }
    this.diag.setDevice(info);
    try {
      this.store = storage(tt, config.mode + '_' + config.apiBaseUrl);
      this.api = new MusicApi(tt, config, this.store);
      this.audio = new FeishuAudio(tt, info, this.diag);
      this.player = new Player({ audio: this.audio, api: this.api, diag: this.diag, persist: p => this.store.set('checkpoint', p) });
    } catch (e) { this.startError = errorCode(e); this.diag.add('unavailable', { code: this.startError }); }
  },
  async connect() {
    if (!this.player) return;
    await this.api.connect();
    const session = await this.api.openQueue();
    if (session.queue.length) this.player.hydrate(session); else this.player.reset();
    this.connected = true;
    return session;
  },
  onShow() { if (this.player) this.player.lifecycle(true); },
  onHide() { if (this.player) this.player.lifecycle(false); }
});
