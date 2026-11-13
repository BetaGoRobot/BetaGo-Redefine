'use strict';
const { ms, fault, errorCode } = require('./safe.js');
class Player {
  constructor(options) {
    this.audio = options.audio; this.api = options.api; this.diag = options.diag;
    this.persist = options.persist || (() => {}); this.now = options.now || Date.now;
    this.notify = []; this.generation = 0; this.loading = false; this.intent = false; this.queueChanging = false;
    this.refreshes = 0; this.endedGeneration = -1; this.lastSave = -Infinity; this.lastObservation = -Infinity; this.hasSource = false;
    this.setTimer = options.setTimer || setTimeout; this.clearTimer = options.clearTimer || clearTimeout;
    this.state = { status: 'idle', queue: [], index: 0, positionMs: 0, durationMs: 0, errorCode: '', saveError: '', expiresAt: 0 };
    this.audio.on('play', () => {
      if (this.queueChanging) { this.audio.pause(); return; }
      if (!this.state.queue.length) { this.audio.pause(); return; }
      if (!this.intent && this.loading) { this.audio.pause(); return; }
      // Native lock-screen Play is also a user action. Resume through a fresh source if expired.
      if (!this.intent && this.state.expiresAt && this.state.expiresAt <= this.now()) {
        this.play().catch(() => {}); return;
      }
      this.intent = true; this.clearWatchdog();
      this.update({ status: 'playing', errorCode: '' });
    });
    this.audio.on('pause', () => { if (!this.loading) { this.intent = false; this.update({ status: 'paused' }); this.save(true); } });
    this.audio.on('stop', () => { if (!this.loading) { this.intent = false; this.update({ status: 'paused' }); this.save(true); } });
    this.audio.on('waiting', () => { if (this.intent) this.update({ status: 'buffering' }); });
    this.audio.on('timeupdate', e => this.time(e));
    this.audio.on('seeked', e => this.time(e));
    this.audio.on('ended', () => {
      if (this.loading || !this.intent || this.endedGeneration === this.generation) return;
      this.endedGeneration = this.generation;
      if (this.state.index + 1 < this.state.queue.length) this.select(this.state.index + 1, 0, true).catch(() => {});
      else { this.intent = false; this.update({ status: 'ended', positionMs: this.state.durationMs }); this.save(true); }
    });
    this.audio.on('error', () => {
      if (this.attaching) { this.pendingMediaError = true; return; }
      if (!this.loading && this.intent) this.recover().catch(() => {});
    });
  }
  subscribe(fn) { this.notify.push(fn); fn(this.snapshot()); return () => { this.notify = this.notify.filter(x => x !== fn); }; }
  snapshot() { return Object.assign({}, this.state, { queue: this.state.queue.slice(), desiredPlaying: this.intent, queueChanging: this.queueChanging }); }
  update(patch) {
    const before = this.state.status; Object.assign(this.state, patch);
    if (before !== this.state.status) this.diag.add('state', { status: this.state.status, index: this.state.index, positionMs: this.state.positionMs });
    this.notify.forEach(fn => fn(this.snapshot()));
  }
  hydrate(session) {
    ++this.generation; this.intent = false; this.clearWatchdog(); this.hasSource = false;
    this.loading = false; this.attaching = false; this.pendingMediaError = false;
    if (!session || !Array.isArray(session.queue) || !session.queue.length) throw fault('EMPTY_QUEUE');
    const queue = session.queue;
    if (queue.some(t => !t || typeof t.id !== 'string' || typeof t.title !== 'string')) throw fault('INVALID_QUEUE');
    const index = Math.max(0, Math.min(queue.length - 1, Math.floor(Number(session.index) || 0)));
    this.update({ queue, index, positionMs: ms(session.positionMs), durationMs: ms(queue[index].durationMs), status: 'paused', expiresAt: 0, errorCode: '' });
  }
  async select(index, positionMs, play, refresh) {
    if (this.queueChanging) return;
    if (!this.state.queue[index]) return;
    const generation = ++this.generation;
    this.clearWatchdog();
    this.loading = true; this.intent = play !== false; this.hasSource = false;
    if (!refresh) this.refreshes = 0;
    this.update({ index, positionMs: ms(positionMs), durationMs: ms(this.state.queue[index].durationMs), status: refresh ? 'refreshing' : 'loading', errorCode: '' });
    // Stop prior track while resolving. Loading ignores native stop/pause events from the transition.
    try {
      this.audio.pause();
      const source = await this.api.resolve(this.state.queue[index].id, !!refresh);
      if (generation !== this.generation) return;
      if (source.expiresAt && source.expiresAt <= this.now() + 1000) throw fault('SOURCE_ALREADY_EXPIRED');
      const durationMs = ms(source.durationMs || this.state.durationMs);
      const pos = ms(this.state.positionMs, durationMs || undefined);
      this.update({ durationMs, positionMs: pos, expiresAt: source.expiresAt || 0 });
      // Keep paused if the user changed intent while resolution was pending.
      if (this.intent) {
        this.watchdog = this.setTimer(() => {
          if (generation !== this.generation || !this.intent || this.state.status === 'playing') return;
          this.loading = false; this.intent = false; this.audio.pause();
          this.update({ status: 'error', errorCode: 'PLAYBACK_TIMEOUT' });
        }, 15000);
        this.attaching = true; this.pendingMediaError = false;
        try { this.audio.load(source, this.state.queue[index], pos); this.hasSource = true; }
        finally { this.attaching = false; }
      }
      this.loading = false;
      if (this.pendingMediaError && this.intent) { this.pendingMediaError = false; return this.recover(); }
      if (!this.intent) this.update({ status: 'paused' });
      this.save(true);
    } catch (e) {
      if (generation !== this.generation) return;
      this.loading = false; this.intent = false;
      this.clearWatchdog();
      this.update({ status: 'error', errorCode: errorCode(e) });
      this.diag.add('load_failed', { code: errorCode(e) });
    }
  }
  async play() {
    if (this.queueChanging) return;
    if (!this.state.queue.length) return;
    if (this.loading) { this.intent = true; return; }
    // Resolve on each explicit resume: URLs can expire while paused or after interruption.
    await this.select(this.state.index, this.state.status === 'ended' ? 0 : this.state.positionMs, true);
  }
  clearWatchdog() { if (this.watchdog) this.clearTimer(this.watchdog); this.watchdog = null; }
  pause() { if (this.queueChanging) return; this.intent = false; this.clearWatchdog(); this.audio.pause(); this.update({ status: 'paused' }); this.save(true); }
  async next(delta) {
    const i = this.state.index + delta;
    if (i < 0 || i >= this.state.queue.length) return;
    return this.select(i, 0, true);
  }
  seek(value) {
    if (this.queueChanging) return;
    const positionMs = ms(value, this.state.durationMs || undefined);
    this.update({ positionMs });
    if (this.hasSource && !this.loading && this.state.status !== 'error') {
      try { this.audio.seek(positionMs); } catch (_) { this.intent = false; this.audio.pause(); this.update({ status: 'error', errorCode: 'SEEK_FAILED' }); }
    }
    this.diag.add('seek', { positionMs }); this.save(true);
  }
  time(e) {
    if (this.loading || this.state.status === 'error') return;
    this.update({ positionMs: ms(e.positionMs), durationMs: ms(e.durationMs || this.state.durationMs) });
    this.save(false);
  }
  async recover() {
    if (this.loading) return;
    if (this.refreshes >= 1) {
      this.intent = false; this.clearWatchdog(); this.audio.pause();
      this.update({ status: 'error', errorCode: 'SOURCE_RETRY_EXHAUSTED' }); this.save(true); return;
    }
    this.refreshes++;
    this.diag.add('source_refresh', { attempt: this.refreshes, positionMs: this.state.positionMs });
    return this.select(this.state.index, this.state.positionMs, this.intent, true);
  }
  lifecycle(visible) { this.diag.add('visibility', { visible }); this.save(true); }
  async replaceQueue(loader) {
    if (this.queueChanging) throw fault('QUEUE_CHANGE_IN_PROGRESS');
    if (this.state.queue.length) this.pause();
    this.intent = false; ++this.generation; this.clearWatchdog();
    this.queueChanging = true; this.loading = true; this.update({});
    const load = (this.saveChain || Promise.resolve()).then(loader);
    // Queue mutations share the progress write barrier. Old-source callbacks are
    // suspended until the new snapshot is installed, so they cannot restore old IDs.
    this.saveChain = load.then(() => {}, () => {});
    try {
      const session = await load;
      if (session && session.queue && session.queue.length) this.hydrate(session); else this.reset();
    } finally {
      this.queueChanging = false; this.loading = false; this.update({});
    }
  }
  reset() {
    ++this.generation; this.intent = false; this.loading = true; this.clearWatchdog();
    this.audio.stop(); this.hasSource = false; this.loading = false;
    this.update({ status: 'idle', queue: [], index: 0, positionMs: 0, durationMs: 0, expiresAt: 0, errorCode: '', saveError: '' });
  }
  save(force) {
    if (this.queueChanging) return;
    if (!this.state.queue.length || (!force && this.now() - this.lastSave < 5000)) return;
    this.lastSave = this.now();
    if (force || this.now() - this.lastObservation >= 30000) {
      this.lastObservation = this.now();
      this.diag.add('progress', { index: this.state.index, positionMs: this.state.positionMs, durationMs: this.state.durationMs });
    }
    const snapshot = { index: this.state.index, positionMs: Math.round(this.state.positionMs), queueIds: this.state.queue.map(t => t.id) };
    try { this.persist(snapshot); } catch (_) { this.diag.add('checkpoint_failed', { code: 'LOCAL_SAVE_FAILED' }); }
    // Serialize writes so a slow older request cannot overwrite newer progress.
    this.saveChain = (this.saveChain || Promise.resolve()).then(() => this.api.saveProgress(snapshot)).then(() => {
      if (this.state.saveError) this.update({ saveError: '' });
    }).catch(e => { this.diag.add('save_failed', { code: errorCode(e) }); this.update({ saveError: errorCode(e) }); });
  }
}
module.exports = { Player };
