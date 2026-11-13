const test = require('node:test'), assert = require('node:assert/strict'), vm = require('node:vm');
const fs = require('node:fs'), path = require('node:path'), { createRequire } = require('node:module');
const { createFixtureServer } = require('../fixtures/server.cjs');
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
  page.toggle(); await until(() => app.player.state.status === 'playing'); assert.equal(page.data.playing, true);
  page.seek({ detail: { value: 133 } }); assert.equal(page.data.position, 133);
  app.onHide(); page.onHide(); page.onUnload(); assert.equal(manager.paused, false, 'hiding/unloading page must retain App audio');
  const second = Object.assign({}, definition, { data: { ...definition.data }, setData(p) { Object.assign(this.data,p); } }); second.onLoad();
  assert.equal(second.data.playing, true); assert.equal(second.data.connected, true, 'recreated page retains active service connection');
  await second.expire(); second.refresh(); await until(() => app.player.state.status === 'playing'); assert.equal(second.data.position, 133);
  second.exportDiagnostics(); await second.logout(); assert.equal(app.api.token, ''); assert.equal(second.data.queue.length, 0);
});
