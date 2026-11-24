const { Diagnostics } = require('../src/lib/diagnostics.js');
const { Player } = require('../src/lib/player.js');
class FakeAudio {
  constructor(autoPlay = true) { this.handlers = {}; this.calls = []; this.autoPlay = autoPlay; }
  on(event, fn) { this.handlers[event] = fn; }
  emit(event, data = {}) { if (this.handlers[event]) this.handlers[event](data); }
  load(source, track, positionMs) { this.calls.push({ action: 'load', source, track, positionMs }); if (this.autoPlay) this.emit('play'); }
  play() { this.calls.push({ action: 'play' }); this.emit('play'); }
  pause() { this.calls.push({ action: 'pause' }); this.emit('pause'); }
  stop() { this.emit('stop'); }
  seek(positionMs) { this.calls.push({ action: 'seek', positionMs }); }
}
function harness(extra = {}) {
  const audio = extra.audio || new FakeAudio(), saves = [], checkpoints = [], timers = [];
  const api = extra.api || { resolve: async id => ({ url: 'https://audio.example/' + id, expiresAt: 9999999999999, durationMs: 960000 }), saveProgress: async p => saves.push(p) };
  const diag = new Diagnostics(() => 10000);
  const player = new Player({ audio, api, diag, now: () => 10000, persist: p => checkpoints.push(p), setTimer: fn => { timers.push(fn); return fn; }, clearTimer: () => {}, ...extra });
  player.hydrate({ queue: [{ id: 'fixture:a', title: 'A', durationMs: 960000 }, { id: 'fixture:b', title: 'B', durationMs: 960000 }], index: 0, positionMs: 0 });
  return { audio, api, player, saves, checkpoints, diag, timers };
}
const flush = () => new Promise(resolve => setImmediate(resolve));
function deferred() { let resolve, reject; const promise = new Promise((a,b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; }
module.exports = { FakeAudio, harness, flush, deferred };
