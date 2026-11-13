'use strict';
const { fault, allowedUrl, originOf, ms } = require('./safe.js');
class MusicApi {
  constructor(tt, config, storage) {
    this.tt = tt; this.config = config; this.storage = storage;
    this.base = config.apiBaseUrl.replace(/\/$/, ''); originOf(this.base, config.allowLocalhost);
    this.token = storage.get('sessionToken') || ''; this.deviceID = storage.get('deviceID');
    if (!this.deviceID) {
      // A device queue key, NOT an authentication credential.
      this.deviceID = 'mini-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 14);
      storage.set('deviceID', this.deviceID);
    }
    this.revision = 0; this.conflict = false;
  }
  request(path, method, data, noAuth) {
    if (!/^\/[a-zA-Z0-9_/?=&%.-]+$/.test(path) || path.indexOf('..') >= 0) return Promise.reject(fault('INVALID_API_PATH'));
    return new Promise((resolve, reject) => {
      const header = { 'Content-Type': 'application/json' };
      if (this.token && !noAuth) header.Authorization = 'Bearer ' + this.token;
      this.tt.request({ url: this.base + path, method: method || 'GET', data,
        header, timeout: this.config.requestTimeoutMs || 10000,
        success: res => {
          if (res.statusCode >= 200 && res.statusCode < 300) { resolve(res.data); return; }
          const status = res.statusCode;
          reject(fault(status === 401 ? 'SESSION_EXPIRED' : status === 409 ? 'QUEUE_CONFLICT' : status === 404 ? 'RESOURCE_UNAVAILABLE' : status === 503 ? 'PROVIDER_UNAVAILABLE' : 'HTTP_' + status));
        }, fail: () => reject(fault('NETWORK_FAILED')) });
    });
  }
  path(tail) { return (this.config.mode === 'fixture' ? '/fixture' : '') + '/music-api/v1' + tail; }
  async connect() {
    if (this.token) {
      // Do not silently replace an expired account with a new empty guest library.
      await this.request(this.path('/me')); return;
    }
    let result;
    if (this.config.authMode === 'feishu') {
      // Current Feishu gadget login uses requestAccess; login is the documented old-client fallback.
      if (!this.config.feishuExchangePath) throw fault('GADGET_AUTH_NOT_CONFIGURED');
      const code = await new Promise((resolve, reject) => {
        const login = this.tt.requestAccess || this.tt.login;
        if (typeof login !== 'function') { reject(fault('LOGIN_UNAVAILABLE')); return; }
        const args = { success: r => r.code ? resolve(r.code) : reject(fault('LOGIN_FAILED')), fail: () => reject(fault('LOGIN_FAILED')) };
        if (this.tt.requestAccess) args.scopeList = [];
        login.call(this.tt, args);
      });
      result = await this.request(this.config.feishuExchangePath, 'POST', { code, transport: 'bearer' }, true);
    } else result = await this.request(this.path('/auth/guest'), 'POST', { transport: 'bearer' }, true);
    if (!result || typeof result.sessionToken !== 'string' || result.sessionToken.length < 16) throw fault('INVALID_SESSION_RESPONSE');
    this.token = result.sessionToken; this.storage.set('sessionToken', this.token);
  }
  async logout() {
    try { await this.request(this.path('/auth/logout'), 'POST', {}); }
    catch (e) { if (e.code !== 'SESSION_EXPIRED') throw e; }
    this.token = ''; this.storage.remove('sessionToken'); this.storage.remove('checkpoint');
  }
  async recoverSession(code) {
    if (typeof code !== 'string' || code.trim().length < 40) throw fault('INVALID_RECOVERY_CODE');
    const r = await this.request(this.path('/auth/recover'), 'POST', { recoveryCode: code.trim(), transport: 'bearer' }, true);
    if (!r || typeof r.sessionToken !== 'string' || r.sessionToken.length < 16) throw fault('INVALID_SESSION_RESPONSE');
    this.token = r.sessionToken; this.storage.set('sessionToken', this.token);
    this.storage.remove('checkpoint');
  }
  queuePath() { return this.path('/queue/' + encodeURIComponent(this.deviceID)); }
  async openQueue() {
    const q = await this.request(this.queuePath());
    this.revision = q.revision; this.conflict = false;
    if (!Array.isArray(q.trackIDs)) throw fault('INVALID_QUEUE');
    if (!q.trackIDs.length) return { queue: [], index: 0, positionMs: 0 };
    const result = await this.request(this.path('/tracks/details'), 'POST', { ids: q.trackIDs });
    const queue = q.trackIDs.map(id => (result.tracks || []).find(t => t.id === id));
    if (queue.some(t => !t)) throw fault('TRACK_METADATA_MISSING');
    return { queue, index: q.currentIndex, positionMs: q.positionMs };
  }
  async playlists() { const r = await this.request(this.path('/playlists')); return r.playlists || []; }
  async search(query) {
    const q = String(query || '').trim();
    if (!q || q.length > 120) throw fault('SEARCH_QUERY_REQUIRED');
    const encoded = encodeURIComponent(q).replace(/[!'()*.]/g, c => '%' + c.charCodeAt(0).toString(16).toUpperCase());
    const r = await this.request(this.path('/search?q=' + encoded));
    if (!r || !Array.isArray(r.tracks) || r.tracks.some(t => !t || typeof t.id !== 'string' || typeof t.title !== 'string')) throw fault('INVALID_SEARCH_RESPONSE');
    return r.tracks.slice(0, 30);
  }
  async useTracks(tracks, index) {
    if (!Array.isArray(tracks) || !tracks.length) throw fault('EMPTY_QUEUE');
    if (tracks.length > 1000) throw fault('QUEUE_LIMIT_REACHED');
    const currentIndex = Math.max(0, Math.min(tracks.length - 1, Math.floor(Number(index) || 0)));
    await this.saveProgress({ queueIds: tracks.map(t => t.id), index: currentIndex, positionMs: 0 });
    return { queue: tracks, index: currentIndex, positionMs: 0 };
  }
  async usePlaylist(id) {
    const list = await this.request(this.path('/playlists/' + encodeURIComponent(id)));
    if (!list.tracks || !list.tracks.length) throw fault('EMPTY_QUEUE');
    await this.saveProgress({ queueIds: list.tracks.map(t => t.id), index: 0, positionMs: 0 });
    return { queue: list.tracks, index: 0, positionMs: 0 };
  }
  async resolve(id) {
    // Each resolve is fresh per shared contract. No Netease cookies/credentials.
    const r = await this.request(this.path('/tracks/' + encodeURIComponent(id) + '/resolve'), 'POST', {});
    if (r.trackID !== id) throw fault('SOURCE_TRACK_MISMATCH');
    if (r.status === 'preview') throw fault('PREVIEW_ONLY');
    if (r.status !== 'available' || !r.url) throw fault('SOURCE_UNAVAILABLE');
    allowedUrl(r.url, this.config.audioOrigins, this.config.allowLocalhost);
    const expiresAt = r.expiresAt ? Date.parse(r.expiresAt) : 0;
    if (r.expiresAt && !Number.isFinite(expiresAt)) throw fault('INVALID_EXPIRY');
    return { url: r.url, expiresAt, durationMs: ms(r.durationMs) };
  }
  async saveProgress(p) {
    if (this.conflict) throw fault('QUEUE_CONFLICT');
    try {
      const r = await this.request(this.queuePath(), 'PUT', { revision: this.revision, trackIDs: p.queueIds, currentIndex: p.index, positionMs: p.positionMs });
      this.revision = r.revision;
    } catch (e) { if (e.code === 'QUEUE_CONFLICT') this.conflict = true; throw e; }
  }
  async expireFixture() {
    if (this.config.mode !== 'fixture') throw fault('FIXTURE_ONLY');
    return this.request('/fixture/control/expire', 'POST', {});
  }
}
module.exports = { MusicApi };
