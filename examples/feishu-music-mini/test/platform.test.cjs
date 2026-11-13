const test = require('node:test'), assert = require('node:assert/strict');
const { FeishuAudio } = require('../src/lib/background-audio.js');
const { Diagnostics } = require('../src/lib/diagnostics.js');
const { allowedUrl } = require('../src/lib/safe.js');
const { openWeb, externalPlayers } = require('../src/lib/fallback.js');
const { storage } = require('../src/lib/storage.js');
test('FIXTURE: official Feishu manager properties and seek use seconds, one listener each', () => {
  const handlers = {}, calls = [], m = { paused: true, currentTime: 12.5, duration: 960, play: () => calls.push('play'), pause: () => {}, seek: s => calls.push(s) };
  ['Canplay','Play','Pause','Stop','Ended','TimeUpdate','Error','Waiting','Seeking','Seeked'].forEach(e => { m['on' + e] = fn => { assert(!handlers[e]); handlers[e] = fn; }; });
  const a = new FeishuAudio({ getBackgroundAudioManager: () => m }, { platform: 'ios', version: '7.49.0' });
  a.load({ url: 'https://audio.test/a' }, { title: 'A' }, 123000);
  assert.equal(m.startTime, 123); assert.equal(m.src, 'https://audio.test/a');
  handlers.Canplay(); assert.equal(calls.at(-1), 123, 'ready-to-play restoration applies documented seconds seek');
  a.seek(456000); assert.equal(calls.at(-1), 456);
  a.load({ url: 'https://audio.test/b' }, { title: 'B' }, 123000); a.seek(789000);
  handlers.Canplay(); assert.equal(calls.at(-1), 789, 'user seek before ready must replace the restore position');
  let progress; a.on('timeupdate', p => { progress = p; }); handlers.TimeUpdate(); assert.equal(progress.positionMs, 12500);
});
test('FIXTURE: PC/H5/unknown/old client rejects background audio without pretending support', () => {
  const tt = { getBackgroundAudioManager: () => { throw new Error('must not call'); } };
  for (const platform of ['windows','mac','devtools','unknown']) assert.throws(() => new FeishuAudio(tt, { platform, version: '9.0.0' }), /MOBILE_GADGET_REQUIRED/);
  assert.throws(() => new FeishuAudio(tt, { platform: 'android', version: '5.19.9' }), /FEISHU_VERSION_UNSUPPORTED/);
});
test('FIXTURE: redaction whitelist and bounded logs exclude credentials and identifiers', () => {
  const d = new Diagnostics(() => 1000);
  d.setDevice({ platform: 'ios', model: 'iPhone 15', system: 'iOS 18', version: '7.49.0', deviceId: 'private-device' });
  for (let i = 0; i < 300; i++) d.add('source_refresh', { code: 'NETWORK_FAILED', url: 'https://private.test/?sig=secret', token: 'secret-token', trackID: 'private-track', index: i });
  const out = d.export('fixture'); assert.equal(d.events.length, 240); assert.doesNotMatch(out, /private|secret|https/); assert.match(out, /NOT_DEVICE_PROOF/);
});
test('FIXTURE: strict origins reject credentials, lookalikes, schemas and localhost in service', async () => {
  for (const url of ['https://audio.test@evil.test/a','https://audio.test.evil.test/a','https://audio.test\\@evil.test/a','javascript:alert(1)','http://audio.test/a']) assert.throws(() => allowedUrl(url, ['https://audio.test'], false));
  assert.equal(allowedUrl('https://audio.test/a?sig=abc', ['https://audio.test'], false), 'https://audio.test/a?sig=abc');
  assert.equal(externalPlayers.length, 0);
  await assert.rejects(() => openWeb({}, { webPlayerUrl: 'https://web.test/?redirect=foo', webOrigins: ['https://web.test'] }), /WEB_URL_QUERY_DENIED/);
});
test('FIXTURE: official storage uses positional arguments and scopes deployments', () => {
  const map = new Map(), tt = { getStorageSync: k => map.get(k), setStorageSync: (k,v) => map.set(k,v), removeStorageSync: k => map.delete(k) };
  const a = storage(tt, 'fixture'), b = storage(tt, 'service'); a.set('sessionToken', 'own-token'); assert.equal(a.get('sessionToken'), 'own-token'); assert.equal(b.get('sessionToken'), undefined);
  a.remove('sessionToken'); assert.equal(a.get('sessionToken'), undefined);
});
