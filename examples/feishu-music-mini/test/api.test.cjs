const test = require('node:test'), assert = require('node:assert/strict');
const { MusicApi } = require('../src/lib/music-api.js');
const { createFixtureServer } = require('../fixtures/server.cjs');
const { Player } = require('../src/lib/player.js');
const { Diagnostics } = require('../src/lib/diagnostics.js');
const { FakeAudio, flush } = require('./helpers.cjs');
function memory() { const m = new Map(); return { get: k => m.get(k), set: (k,v) => m.set(k,v), remove: k => m.delete(k) }; }
function fetchTt() { return { request: o => {
  const headers = o.header;
  fetch(o.url, { method: o.method, headers, body: o.data === undefined ? undefined : JSON.stringify(o.data), signal: AbortSignal.timeout(o.timeout) }).then(async r => {
    o.success({ statusCode: r.status, data: await r.json() });
  }).catch(o.fail);
} }; }
async function setup(t) {
  const server = createFixtureServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const origin = 'http://127.0.0.1:' + server.address().port;
  const config = { mode: 'fixture', apiBaseUrl: origin, audioOrigins: [origin], allowLocalhost: true, authMode: 'anonymous', requestTimeoutMs: 3000 };
  const store = memory(), api = new MusicApi(fetchTt(), config, store);
  await api.connect(); return { server, api, config, store };
}
test('HTTP FIXTURE: bearer session, metadata, duplicate queue items and CAS persist/restore', async t => {
  const { api, config, store } = await setup(t);
  let q = await api.openQueue(); assert.equal(q.queue.length, 2); assert.equal(q.queue[0].durationMs, 960000);
  await api.saveProgress({ queueIds: [q.queue[0].id, q.queue[0].id, q.queue[1].id], index: 1, positionMs: 720000 });
  const restored = new MusicApi(fetchTt(), config, store); await restored.connect(); q = await restored.openQueue();
  assert.equal(q.queue.length, 3); assert.equal(q.index, 1); assert.equal(q.positionMs, 720000);
  assert.equal((await api.playlists()).length, 1);
});
test('HTTP FIXTURE: stale revision stops writes until explicit queue reload', async t => {
  const { api, config, store } = await setup(t);
  const q = await api.openQueue(), other = new MusicApi(fetchTt(), config, store); await other.openQueue();
  const p = { queueIds: q.queue.map(t => t.id), index: 1, positionMs: 42000 };
  await other.saveProgress(p); await assert.rejects(() => api.saveProgress(p), /QUEUE_CONFLICT/);
  await assert.rejects(() => api.saveProgress(p), /QUEUE_CONFLICT/);
  const remote = await api.openQueue(); assert.equal(remote.positionMs, 42000);
});
test('HTTP FIXTURE: real Range request and expired URL 403; simulated manager resumes same position from renewed source', async t => {
  const { api } = await setup(t), q = await api.openQueue();
  const audio = new FakeAudio(), diag = new Diagnostics();
  const player = new Player({ api, audio, diag }); player.hydrate(q);
  await player.play(); const first = audio.calls.at(-1).source.url;
  const range = await fetch(first, { headers: { Range: 'bytes=0-43' } });
  assert.equal(range.status, 206, 'generate fixture WAVs first'); assert.equal(range.headers.get('accept-ranges'), 'bytes');
  const bytes = Buffer.from(await range.arrayBuffer()); assert.equal(bytes.toString('ascii', 0, 4), 'RIFF'); assert.equal(bytes.readUInt32LE(24), 8000);
  audio.emit('timeupdate', { positionMs: 133000, durationMs: 960000 });
  await api.expireFixture(); assert.equal((await fetch(first)).status, 403);
  audio.emit('error'); await flush();
  for (let i = 0; i < 20 && audio.calls.filter(c => c.action === 'load').length < 2; i++) await new Promise(r => setTimeout(r, 10));
  assert.equal(player.state.status, 'playing'); const second = audio.calls.at(-1);
  assert.notEqual(second.source.url, first); assert.equal(second.positionMs, 133000);
  const renewed = await fetch(second.source.url, { headers: { Range: 'bytes=44-63' } }); assert.equal(renewed.status, 206); await renewed.arrayBuffer();
  await player.saveChain;
});
test('HTTP FIXTURE: invalid bearer never silently creates a different guest identity', async t => {
  const { api, config, store } = await setup(t); await api.logout();
  store.set('sessionToken', 'invalid-old-token');
  const expired = new MusicApi(fetchTt(), config, store);
  await assert.rejects(() => expired.connect(), /SESSION_EXPIRED/);
});
test('HTTP FIXTURE: search existing API and append a selected track using device queue CAS', async t => {
  const { api } = await setup(t);
  const session = await api.openQueue();
  const results = await api.search('合成音 A');
  assert.equal(results.length, 1); assert.equal(results[0].id, session.queue[0].id);
  const next = await api.useTracks(session.queue.concat(results[0]), 2);
  assert.equal(next.index, 2); assert.equal(next.queue.length, 3);
  const restored = await api.openQueue(); assert.equal(restored.index, 2); assert.equal(restored.queue.length, 3);
});
test('CONTRACT: song searches preserve apostrophes, live labels and repeated dots', async () => {
  let requested;
  const api = new MusicApi({ request: o => { requested = o.url; o.success({ statusCode: 200, data: { tracks: [] } }); } }, { apiBaseUrl: 'https://api.test', mode: 'service' }, memory());
  const query = "Don't Stop (Live)...";
  assert.deepEqual(await api.search(query), []);
  assert.equal(new URL(requested).searchParams.get('q'), query);
});
test('CONTRACT: current gadget authorization exchanges code via own service using bearer transport', async () => {
  const requests = [], tt = { requestAccess: o => { assert.deepEqual(o.scopeList, []); o.success({ code: 'one-use-code' }); },
    request: o => { requests.push(o); o.success({ statusCode: 200, data: { sessionToken: 'own-service-token-1234567890' } }); } };
  const api = new MusicApi(tt, { apiBaseUrl: 'https://api.test', mode: 'service', authMode: 'feishu', feishuExchangePath: '/music-api/v1/auth/feishu/exchange' }, memory());
  await api.connect(); assert.equal(requests[0].url, 'https://api.test/music-api/v1/auth/feishu/exchange');
  assert.deepEqual(requests[0].data, { code: 'one-use-code', transport: 'bearer' });
  assert.equal(requests[0].header.Authorization, undefined);
});
test('CONTRACT: preview, wrong track, untrusted source and absent source remain errors', async () => {
  for (const [response, code] of [
    [{ trackID: 'a', status: 'preview', url: 'https://audio.test/a' }, 'PREVIEW_ONLY'],
    [{ trackID: 'b', status: 'available', url: 'https://audio.test/a' }, 'SOURCE_TRACK_MISMATCH'],
    [{ trackID: 'a', status: 'available', url: 'https://evil.test/a' }, 'URL_ORIGIN_DENIED'],
    [{ trackID: 'a', status: 'unavailable' }, 'SOURCE_UNAVAILABLE']
  ]) {
    const api = new MusicApi({ request: o => o.success({ statusCode: 200, data: response }) }, { apiBaseUrl: 'https://api.test', audioOrigins: ['https://audio.test'] }, memory());
    await assert.rejects(() => api.resolve('a'), new RegExp(code));
  }
});
