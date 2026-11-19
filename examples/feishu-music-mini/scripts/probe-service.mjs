// Optional integration probe against the OTHER task's isolated test server.
// Uses a new own guest session; logs statuses only, never credentials or user IDs.
import { createRequire } from 'node:module';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url), { MusicApi } = require('../src/lib/music-api.js');
const { tracks } = require('../fixtures/server.cjs');
const origin = process.env.MUSIC_TEST_ORIGIN;
assert(origin, 'MUSIC_TEST_ORIGIN is required; point only at an isolated music API test service');
const tt = { request: o => fetch(o.url, { method: o.method, headers: o.header, body: o.data === undefined ? undefined : JSON.stringify(o.data), signal: AbortSignal.timeout(o.timeout) }).then(async r => o.success({ statusCode: r.status, data: await r.json() })).catch(o.fail) };
const map = new Map(), store = { get: k => map.get(k), set: (k,v) => map.set(k,v), remove: k => map.delete(k) };
// HTTP is allowed ONLY in this Node probe, never in a service gadget build.
const api = new MusicApi(tt, { apiBaseUrl: origin, allowLocalhost: true, mode: 'service', authMode: 'anonymous', requestTimeoutMs: 5000 }, store);
await api.connect(); const q = await api.openQueue(); const lists = await api.playlists();
assert.equal(q.queue.length, 0); assert.equal(lists.length, 0);
await api.saveProgress({ queueIds: [], index: 0, positionMs: 0 });
await api.openQueue();
await api.request(api.path('/import'), 'POST', { format: 'betago-music', version: 1, playlists: [{ name: '[MINI INTEGRATION FIXTURE] CC0 metadata only', tracks }], likes: [] });
const list = (await api.playlists())[0]; assert(list);
const selected = await api.usePlaylist(list.id); assert.equal(selected.queue.length, 2);
await api.saveProgress({ queueIds: tracks.map(t => t.id), index: 1, positionMs: 133000 });
const restored = await api.openQueue(); assert.equal(restored.index, 1); assert.equal(restored.positionMs, 133000);
assert.deepEqual(restored.queue.map(t => t.id), tracks.map(t => t.id));
await assert.rejects(() => api.resolve(tracks[0].id), /SOURCE_UNAVAILABLE/, 'real default provider must reject synthetic source rather than silently substitute music');
const recovery = await api.request(api.path('/auth/recovery'), 'POST', {});
const recoveredMap = new Map([['deviceID',api.deviceID]]), recoveredStore = { get: k => recoveredMap.get(k), set: (k,v) => recoveredMap.set(k,v), remove: k => recoveredMap.delete(k) };
const other = new MusicApi(tt, { apiBaseUrl: origin, allowLocalhost: true, mode: 'service', authMode: 'anonymous', requestTimeoutMs: 5000 }, recoveredStore);
await other.recoverSession(recovery.recoveryCode);
assert.equal((await other.playlists()).length, 1); assert.equal((await other.openQueue()).positionMs, 133000);
await other.logout();
await api.request(api.path('/playlists/' + encodeURIComponent(list.id)), 'DELETE', { revision: list.revision });
await api.saveProgress({ queueIds: [], index: 0, positionMs: 0 }); await api.logout();
console.log(JSON.stringify({ service: 'SELF_HOSTED_MUSIC_API', newBearerSession: 'PASS', emptyQueueAndLibrary: 'PASS', existingPlaylistLoad: 'PASS', stableMetadataMapping: 'PASS', queueCASRoundtrip: 'PASS', ownRecoverySession: 'PASS', unsupportedSourceVisible: 'PASS', testPlaylistDeleted: 'PASS', logout: 'PASS', realAudio: 'NOT_TESTED', feishuDevice: 'NOT_TESTED' }));
