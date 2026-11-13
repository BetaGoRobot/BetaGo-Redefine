const test = require('node:test'), assert = require('node:assert/strict'), vm = require('node:vm');
const fs = require('node:fs'), path = require('node:path'), { createRequire } = require('node:module');
const { createFixtureServer } = require('../fixtures/server.cjs');
const { MusicApi } = require('../src/lib/music-api.js');
const { Player } = require('../src/lib/player.js');
const { Diagnostics } = require('../src/lib/diagnostics.js');
const { FakeAudio } = require('./helpers.cjs');
async function until(fn) { for (let i = 0; i < 100 && !fn(); i++) await new Promise(r => setTimeout(r, 5)); assert(fn()); }
test('VM FIXTURE: complete App/Page connect, play, seek, background, recreate page, expire and logout', async t => {
  const server = createFixtureServer(); await new Promise(r => server.listen(0,'127.0.0.1',r));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const origin = 'http://127.0.0.1:' + server.address().port, events = {}, map = new Map();
  const manager = { paused: true, currentTime: 0, duration: 960, play() { this.paused = false; events.Play(); }, pause() { this.paused = true; events.Pause(); }, stop() { this.paused = true; events.Stop(); }, seek(s) { this.currentTime = s; events.Seeked(); } };
  ['Canplay','Play','Pause','Stop','Ended','TimeUpdate','Error','Waiting','Seeking','Seeked'].forEach(k => { manager['on' + k] = fn => { events[k] = fn; }; });
  const tt = { getSystemInfoSync: () => ({ platform: 'android', version: '7.49.0', model: 'VM FIXTURE' }), getBackgroundAudioManager: () => manager,
    getStorageSync: k => map.get(k), setStorageSync: (k,v) => map.set(k,v), removeStorageSync: k => map.delete(k), setClipboardData: o => assert.doesNotMatch(o.data, /Bearer|sessionToken|https:/),
    request: o => fetch(o.url, { method: o.method, headers: o.header, body: o.data === undefined ? undefined : JSON.stringify(o.data) }).then(async r => o.success({ statusCode: r.status, data: await r.json() })).catch(o.fail) };
  let app, definition;
  const config = { mode: 'fixture', apiBaseUrl: origin, audioOrigins: [origin], allowLocalhost: true, authMode: 'anonymous' };
  const appFile = path.resolve('src/app.js'), realRequire = createRequire(appFile);
  vm.runInNewContext(fs.readFileSync(appFile, 'utf8'), { require: id => id === './config.js' ? config : realRequire(id), App: o => { app = o; }, tt, setTimeout, clearTimeout });
  app.onLaunch(); assert.equal(app.startError, undefined);
  const pageFile = path.resolve('src/pages/player/index.js');
  vm.runInNewContext(fs.readFileSync(pageFile,'utf8'), { require: createRequire(pageFile), Page: o => { definition = o; }, getApp: () => app, tt });
  const page = Object.assign({}, definition, { data: { ...definition.data }, setData(p) { Object.assign(this.data,p); } });
  page.onLoad(); await page.connect(); assert.equal(page.data.queue.length, 2); assert.equal(page.data.playing, false);
  page.searchInput({ detail: { value: '合成音 A' } }); await page.searchTracks();
  assert.equal(page.data.searchResults.length, 1);
  page.toggle(); await until(() => app.player.state.status === 'playing'); assert.equal(page.data.playing, true);
  page.seek({ detail: { value: 133 } }); assert.equal(page.data.position, 133);
  app.onHide(); page.onHide(); page.onUnload(); assert.equal(manager.paused, false, 'hiding/unloading page must retain App audio');
  const second = Object.assign({}, definition, { data: { ...definition.data }, setData(p) { Object.assign(this.data,p); } }); second.onLoad();
  assert.equal(second.data.playing, true); assert.equal(second.data.connected, true, 'recreated page retains active service connection');
  await second.expire(); second.refresh(); await until(() => app.player.state.status === 'playing'); assert.equal(second.data.position, 133);
  second.searchInput({ detail: { value: '合成音 B' } }); await second.searchTracks();
  await second.playSearchResult({ currentTarget: { dataset: { index: 0 } } });
  assert.equal(app.player.state.index, 1); assert.equal(app.player.state.status, 'playing');
  second.exportDiagnostics(); await second.logout(); assert.equal(app.api.token, ''); assert.equal(second.data.queue.length, 0);
});
test('VM CONTRACT: recovery metadata failure cannot save the prior account queue into the recovered account', async () => {
  const oldToken = 'old-test-session-1234567890', recoveredToken = 'recovered-test-session-1234567890';
  const map = new Map([['sessionToken', oldToken], ['deviceID', 'recovery-device']]);
  const store = { get: key => map.get(key), set: (key, value) => map.set(key, value), remove: key => map.delete(key) };
  const recoveredWrites = []; let metadataRequests = 0;
  const tt = { request: o => {
    const route = new URL(o.url).pathname;
    if (route.endsWith('/auth/recover')) return o.success({ statusCode: 200, data: { sessionToken: recoveredToken } });
    if (route.endsWith('/me')) return o.success({ statusCode: 200, data: { user: { id: 'recovered-test-user' } } });
    if (route.endsWith('/queue/recovery-device') && o.method === 'GET') {
      return o.success({ statusCode: 200, data: { revision: 7, trackIDs: ['fixture:recovered'], currentIndex: 0, positionMs: 0 } });
    }
    if (route.endsWith('/tracks/details')) {
      metadataRequests++;
      return o.success({ statusCode: 503, data: {} });
    }
    if (route.endsWith('/queue/recovery-device') && o.method === 'PUT') {
      if (o.header.Authorization === 'Bearer ' + recoveredToken) recoveredWrites.push(o.data);
      return o.success({ statusCode: 200, data: { revision: o.data.revision + 1 } });
    }
    assert.fail('unexpected request: ' + route);
  } };
  const config = { mode: 'service', apiBaseUrl: 'https://api.test' }, diag = new Diagnostics();
  const api = new MusicApi(tt, config, store), player = new Player({ api, audio: new FakeAudio(), diag });
  player.hydrate({ queue: [{ id: 'fixture:prior-account', title: 'Prior account song', durationMs: 100000 }], index: 0, positionMs: 10000 });
  let app, definition;
  const appFile = path.resolve('src/app.js'), realRequire = createRequire(appFile);
  vm.runInNewContext(fs.readFileSync(appFile, 'utf8'), { require: id => id === './config.js' ? config : realRequire(id), App: o => { app = o; } });
  Object.assign(app, { api, player, diag, connected: true });
  const pageFile = path.resolve('src/pages/player/index.js');
  vm.runInNewContext(fs.readFileSync(pageFile, 'utf8'), { require: createRequire(pageFile), Page: o => { definition = o; }, getApp: () => app, tt });
  const page = Object.assign({}, definition, { data: { ...definition.data }, setData(p) { Object.assign(this.data, p); } });
  page.onLoad(); page.recoveryInput({ detail: { value: 'test-recovery-code-'.repeat(4) } });
  await page.recoverSession();
  assert.equal(api.token, recoveredToken, 'recovery succeeded before metadata failed');
  assert.equal(metadataRequests, 1); assert.notEqual(page.data.error, '');
  page.seek({ detail: { value: 20 } }); app.onHide(); await player.saveChain;
  assert.equal(recoveredWrites.length, 0, 'old-account track IDs must never be written using the recovered session');
  assert.equal(player.state.queue.length, 0); assert.equal(page.data.queue.length, 0);
  assert.equal(app.connected, false); assert.equal(page.data.connected, false);
  assert.equal(page.data.hasSession, true, 'the recovered session remains available for an explicit reconnect');
});
test('VM CONTRACT: reconnect metadata failure cannot overwrite the newer remote queue after a conflict', async () => {
  const store = { get: key => key === 'sessionToken' ? 'existing-test-session-1234567890' : key === 'deviceID' ? 'reconnect-device' : undefined, set() {}, remove() {} };
  const writes = []; let metadataRequests = 0;
  const tt = { request: o => {
    const route = new URL(o.url).pathname;
    if (route.endsWith('/me')) return o.success({ statusCode: 200, data: { user: { id: 'existing-test-user' } } });
    if (route.endsWith('/queue/reconnect-device') && o.method === 'GET') {
      return o.success({ statusCode: 200, data: { revision: 7, trackIDs: ['fixture:newer-remote'], currentIndex: 0, positionMs: 0 } });
    }
    if (route.endsWith('/tracks/details')) {
      metadataRequests++;
      return o.success({ statusCode: 503, data: {} });
    }
    if (route.endsWith('/queue/reconnect-device') && o.method === 'PUT') {
      writes.push(o.data);
      return o.success({ statusCode: 200, data: { revision: o.data.revision + 1 } });
    }
    assert.fail('unexpected request: ' + route);
  } };
  const config = { mode: 'service', apiBaseUrl: 'https://api.test' }, diag = new Diagnostics();
  const api = new MusicApi(tt, config, store), player = new Player({ api, audio: new FakeAudio(), diag });
  player.hydrate({ queue: [{ id: 'fixture:stale-local', title: 'Stale local song', durationMs: 100000 }], index: 0, positionMs: 10000 });
  api.revision = 1; api.conflict = true;
  let app, definition;
  const appFile = path.resolve('src/app.js'), realRequire = createRequire(appFile);
  vm.runInNewContext(fs.readFileSync(appFile, 'utf8'), { require: id => id === './config.js' ? config : realRequire(id), App: o => { app = o; } });
  Object.assign(app, { api, player, diag, connected: true });
  const pageFile = path.resolve('src/pages/player/index.js');
  vm.runInNewContext(fs.readFileSync(pageFile, 'utf8'), { require: createRequire(pageFile), Page: o => { definition = o; }, getApp: () => app, tt });
  const page = Object.assign({}, definition, { data: { ...definition.data }, setData(p) { Object.assign(this.data, p); } });
  page.onLoad(); await page.connect();
  assert.equal(metadataRequests, 1); assert.equal(api.revision, 7); assert.notEqual(page.data.error, '');
  page.seek({ detail: { value: 20 } }); app.onHide(); await player.saveChain;
  assert.equal(writes.length, 0, 'failed reload must not write stale local IDs at the newer remote revision');
  assert.equal(player.state.queue.length, 0); assert.equal(page.data.queue.length, 0);
  assert.equal(app.connected, false); assert.equal(page.data.connected, false); assert.equal(page.data.hasSession, true);
});
