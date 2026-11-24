const test = require('node:test'), assert = require('node:assert/strict');
const { harness, FakeAudio, flush, deferred } = require('./helpers.cjs');

test('FIXTURE: hydrate restores paused; explicit playback loads at saved position', async () => {
  const { player, audio } = harness();
  player.hydrate({ queue: player.state.queue, index: 1, positionMs: 123000 });
  assert.equal(player.state.status, 'paused'); assert.equal(audio.calls.length, 0);
  await player.play(); assert.equal(player.state.status, 'playing');
  assert.equal(audio.calls.at(-1).positionMs, 123000);
});
test('FIXTURE: ended advances exactly once; queue end stops without wrap', async () => {
  const { player, audio } = harness(); await player.play();
  audio.emit('ended'); audio.emit('ended'); await flush();
  assert.equal(player.state.index, 1);
  assert.equal(audio.calls.filter(c => c.action === 'load').length, 2);
  audio.emit('ended'); await flush(); assert.equal(player.state.status, 'ended');
});
test('FIXTURE: latest track wins slow resolver race', async () => {
  const a = deferred(), b = deferred();
  const { player, audio } = harness({ api: { resolve: id => id.endsWith(':a') ? a.promise : b.promise, saveProgress: async () => {} } });
  const p = player.play(), n = player.next(1);
  b.resolve({ url: 'https://audio.test/b' }); await n;
  a.resolve({ url: 'https://audio.test/a' }); await p;
  assert.equal(player.state.index, 1); assert.equal(audio.calls.filter(c => c.action === 'load').length, 1);
  assert.equal(audio.calls.at(-1).track.id, 'fixture:b');
});
test('FIXTURE: pause while resolving never starts playback', async () => {
  const d = deferred(); const { player, audio } = harness({ api: { resolve: () => d.promise, saveProgress: async () => {} } });
  const p = player.play(); player.pause(); d.resolve({ url: 'https://audio.test/a' }); await p;
  assert.equal(player.state.status, 'paused'); assert.equal(audio.calls.filter(c => c.action === 'load').length, 0);
});
test('FIXTURE: seek during resolver load is preserved and clamped', async () => {
  const d = deferred(); const { player, audio } = harness({ api: { resolve: () => d.promise, saveProgress: async () => {} } });
  const p = player.play(); player.seek(123000); d.resolve({ url: 'https://audio.test/a', durationMs: 200000 }); await p;
  assert.equal(audio.calls.at(-1).positionMs, 123000);
  player.seek(9999999); assert.equal(player.state.positionMs, 200000);
});
test('FIXTURE: media error resolves fresh source once and restores last position; second failure stops', async () => {
  let count = 0;
  const { player, audio } = harness({ api: { resolve: async () => ({ url: 'https://audio.test/' + ++count }), saveProgress: async () => {} } });
  await player.play(); audio.emit('timeupdate', { positionMs: 74123, durationMs: 960000 });
  audio.emit('error'); await flush();
  assert.equal(count, 2); assert.equal(audio.calls.at(-1).positionMs, 74123);
  assert.equal(player.state.status, 'playing');
  audio.emit('error'); await flush();
  assert.equal(count, 2); assert.equal(player.state.errorCode, 'SOURCE_RETRY_EXHAUSTED');
});
test('FIXTURE: native pause/interruption does not auto-resume; user resume refreshes source', async () => {
  const { player, audio } = harness(); await player.play();
  audio.emit('timeupdate', { positionMs: 84000 }); audio.emit('pause');
  assert.equal(player.state.status, 'paused'); assert.equal(player.intent, false);
  player.lifecycle(false); player.lifecycle(true); assert.equal(player.intent, false);
  await player.play(); assert.equal(audio.calls.at(-1).positionMs, 84000);
});
test('FIXTURE: native lock screen Play resumes paused state', async () => {
  const { player, audio } = harness(); await player.play(); player.pause(); audio.emit('play');
  assert.equal(player.intent, true); assert.equal(player.state.status, 'playing');
});
test('FIXTURE: source expiry is rejected and original resolver error is sanitized', async () => {
  const { player } = harness({ api: { resolve: async () => ({ url: 'https://audio.test', expiresAt: 9000 }), saveProgress: async () => {} } });
  await player.play(); assert.equal(player.state.errorCode, 'SOURCE_ALREADY_EXPIRED');
});
test('FIXTURE: source and queue metadata failure stays visible', async () => {
  const { player } = harness({ api: { resolve: async () => { throw new Error('secret=https://audio.test/?token=foo'); }, saveProgress: async () => {} } });
  await player.play(); assert.equal(player.state.errorCode, 'OPERATION_FAILED');
  assert.doesNotMatch(player.diag.export(), /token|https/);
});
test('FIXTURE: playback watchdog exposes missing native start event', async () => {
  const { player, timers } = harness({ audio: new FakeAudio(false) });
  await player.play(); timers.at(-1)(); assert.equal(player.state.errorCode, 'PLAYBACK_TIMEOUT');
});
test('FIXTURE: checkpoints are throttled and API writes serialized, with visible failures', async () => {
  const d = deferred(); let calls = 0;
  const { player, audio, checkpoints } = harness({ api: { resolve: async () => ({ url: 'https://audio.test/a' }), saveProgress: async () => { calls++; if (calls === 1) await d.promise; else throw Object.assign(new Error(), { code: 'QUEUE_CONFLICT' }); } } });
  await player.play(); audio.emit('timeupdate', { positionMs: 1000 }); audio.emit('timeupdate', { positionMs: 2000 }); player.lifecycle(false);
  await flush(); assert.equal(calls, 1); assert.equal(checkpoints.length, 2);
  d.resolve(); await player.saveChain; assert.equal(calls, 2); assert.equal(player.state.saveError, 'QUEUE_CONFLICT');
});
test('FIXTURE: seek before first play updates restore position without touching an unloaded manager', async () => {
  const { player, audio } = harness(); player.seek(30000);
  assert.equal(audio.calls.length, 0); await player.play(); assert.equal(audio.calls.at(-1).positionMs, 30000);
});
test('FIXTURE: synchronous native load error retries once and terminates', async () => {
  const audio = new FakeAudio(false);
  audio.load = function () { this.calls.push({ action: 'load' }); this.emit('error'); };
  const { player } = harness({ audio }); await player.play();
  assert.equal(audio.calls.filter(c => c.action === 'load').length, 2);
  assert.equal(player.state.errorCode, 'SOURCE_RETRY_EXHAUSTED');
});
test('FIXTURE: logout/reset makes old native resume inert', async () => {
  const { player, audio } = harness(); await player.play(); player.reset(); audio.emit('play');
  assert.equal(player.state.queue.length, 0); assert.equal(player.intent, false);
});
test('FIXTURE: reloading queue cancels a pending resolver without leaving loading latched', async () => {
  const d = deferred(); let count = 0;
  const { player, audio } = harness({ api: { resolve: () => ++count === 1 ? d.promise : Promise.resolve({ url: 'https://audio.test/b' }), saveProgress: async () => {} } });
  const old = player.play();
  player.hydrate({ queue: player.state.queue, index: 1, positionMs: 60000 });
  await player.play(); assert.equal(player.state.status, 'playing'); assert.equal(audio.calls.at(-1).track.id, 'fixture:b');
  d.resolve({ url: 'https://audio.test/a' }); await old;
  assert.equal(player.state.index, 1); assert.equal(player.state.positionMs, 60000);
});
test('FIXTURE: changing songs waits for prior saves and blocks stale seek/progress writes', async () => {
  const pendingSave = deferred(), pendingQueue = deferred(); let writes = 0, loaderStarted = false;
  const { player, audio } = harness({ api: { resolve: async () => ({ url: 'https://audio.test/a' }), saveProgress: async () => { writes++; if (writes === 1) await pendingSave.promise; } } });
  await player.play();
  const original = player.state.queue.slice();
  const changed = player.replaceQueue(async () => { loaderStarted = true; await pendingQueue.promise; return { queue: original.concat({ id: 'fixture:c', title: 'C' }), index: 2, positionMs: 0 }; });
  await flush(); assert.equal(loaderStarted, false);
  pendingSave.resolve(); await flush(); assert.equal(loaderStarted, true);
  const before = writes;
  player.seek(123000); player.lifecycle(false); audio.emit('timeupdate', { positionMs: 456000 });
  await player.play(); await flush();
  assert.equal(writes, before); assert.equal(player.state.positionMs, 0);
  pendingQueue.resolve(); await changed;
  assert.equal(player.state.index, 2); assert.equal(player.state.queue.length, 3);
  assert.equal(player.state.status, 'paused'); assert.equal(player.snapshot().queueChanging, false);
});
