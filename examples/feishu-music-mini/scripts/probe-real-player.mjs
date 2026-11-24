// Live API + exact built gadget modules; the native manager is simulated.
// Never use this report as Feishu device, audio decoding or lock-screen proof.
import { createRequire } from 'node:module';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import assert from 'node:assert/strict';
const args = process.argv.slice(2), waitExpiry = args.includes('--wait-expiry');
const configArg = args.indexOf('--config');
const configPath = resolve(configArg < 0 ? 'dist/gadget/config.js' : args[configArg + 1]);
const require = createRequire(configPath);
const config = require(configPath);
const { MusicApi } = require('./lib/music-api.js');
const { FeishuAudio } = require('./lib/background-audio.js');
const { Player } = require('./lib/player.js');
const { Diagnostics } = require('./lib/diagnostics.js');
const { allowedUrl, errorCode } = require('./lib/safe.js');
const report = { checkedAt: new Date().toISOString(), proof: 'LIVE_API_WITH_SIMULATED_NATIVE_MANAGER',
  officialCompilerExecuted: false, nativeFeishuDeviceExecuted: false, apiOrigin: config.apiBaseUrl,
  audioOrigins: config.audioOrigins, appId: config.appId, authMode: config.authMode, tracks: [], nativeLoads: [] };
const events = {}, sources = new Map(), lastResolutions = new Map();
let phase = 'configuration', api, player, resolveCount = 0;
const manager = {
  paused: true, currentTime: 0, duration: 0, startTime: 0,
  set src(value) {
    allowedUrl(value, config.audioOrigins, false);
    this.source = value;
    const source = sources.get(value); assert(source);
    this.duration = source.durationMs / 1000;
    report.nativeLoads.push({ trackID: source.trackID, origin: new URL(value).origin, startTime: this.startTime, reason: source.reason });
  },
  get src() { return this.source; },
  play() { this.paused = false; this.currentTime = this.startTime; events.Play(); },
  pause() { this.paused = true; if (events.Pause) events.Pause(); },
  stop() { this.paused = true; if (events.Stop) events.Stop(); },
  seek(seconds) { this.currentTime = seconds; events.Seeked(); }
};
['Canplay','Play','Pause','Stop','Ended','TimeUpdate','Error','Waiting','Seeking','Seeked'].forEach(name => {
  manager['on' + name] = callback => { events[name] = callback; };
});
const tt = { getBackgroundAudioManager: () => manager, request: options => {
  fetch(options.url, { method: options.method, headers: options.header, redirect: 'error',
    body: options.data === undefined ? undefined : JSON.stringify(options.data), signal: AbortSignal.timeout(options.timeout) })
    .then(async response => {
      const data = await response.json();
      if (new URL(options.url).pathname.endsWith('/resolve') && response.ok) {
        resolveCount++;
        lastResolutions.set(data.trackID, data);
        if (data.url) sources.set(data.url, data);
      }
      options.success({ statusCode: response.status, data });
    }).catch(() => options.fail());
} };
const map = new Map(), storage = { get: key => map.get(key), set: (key, value) => map.set(key, value), remove: key => map.delete(key) };
const sleep = ms => new Promise(done => setTimeout(done, ms));
async function settled(check) {
  const deadline = Date.now() + 15000;
  while (!check() && Date.now() < deadline) await sleep(20);
  assert(check(), 'PLAYER_TRANSITION_TIMEOUT');
}
async function range(url, expected = 206) {
  allowedUrl(url, config.audioOrigins, false);
  const response = await fetch(url, { headers: { Range: 'bytes=0-1023' }, redirect: 'manual', signal: AbortSignal.timeout(15000) });
  const sample = Buffer.from(await response.arrayBuffer());
  assert.equal(response.status, expected, 'MEDIA_HTTP_STATUS');
  if (expected !== 206) return { http: response.status };
  const mp3Header = sample.subarray(0, 3).toString() === 'ID3' || (sample[0] === 255 && (sample[1] & 224) === 224);
  assert(mp3Header, 'MP3_HEADER_REQUIRED');
  return { http: response.status, origin: new URL(url).origin, contentType: response.headers.get('content-type'),
    contentRange: response.headers.get('content-range'), sampleBytes: sample.length, mp3Header };
}
try {
  assert.equal(config.mode, 'service'); assert.equal(config.allowLocalhost, false);
  assert.equal(config.authMode, 'anonymous'); assert.match(config.appId, /^cli_[A-Za-z0-9]+$/);
  const project = JSON.parse(await readFile(resolve(dirname(configPath), '../project.config.json'), 'utf8'));
  assert.equal(project.appid, config.appId);
  report.appIdMatchesProject = true;
  api = new MusicApi(tt, config, storage);
  phase = 'auth_options';
  const auth = await api.request(api.path('/auth/options'), 'GET', undefined, true);
  report.authOptions = { guest: auth.guest, feishuEnabled: auth.feishu && auth.feishu.enabled };
  assert(auth.guest);
  phase = 'guest_me_queue';
  await api.connect(); await api.request(api.path('/me')); await api.openQueue();
  report.guestAndMe = 'PASS';
  const metadata = [];
  for (const [id, title] of [['netease:109616', '100种生活'], ['netease:1417862065', '小王日记']]) {
    phase = 'search_metadata_lyrics';
    const found = (await api.search(title)).find(track => track.id === id);
    assert(found, 'SEARCH_RECORDING_MISSING');
    const details = await api.request(api.path('/tracks/' + encodeURIComponent(id)));
    assert.equal(details.id, id); metadata.push(details);
    const lyrics = await api.request(api.path('/tracks/' + encodeURIComponent(id) + '/lyrics'));
    assert.equal(lyrics.trackID, id);
    report.tracks.push({ id, title: details.title, durationMs: details.durationMs,
      searchFound: true, metadata: 'PASS', lyrics: { http: 200, lrcChars: (lyrics.lrc || '').length } });
  }
  const diag = new Diagnostics();
  const audio = new FeishuAudio(tt, { platform: 'android', version: '7.49.0' }, diag);
  player = new Player({ api, audio, diag });
  phase = 'queue_and_native_adapter';
  player.hydrate(await api.useTracks(metadata, 0));
  await player.play(); assert.equal(player.state.status, 'playing');
  for (let i = 0; i < metadata.length; i++) {
    if (i) { events.Ended(); await settled(() => player.state.index === i && player.state.status === 'playing'); }
    const row = report.tracks[i], source = lastResolutions.get(row.id);
    assert.equal(source.status, 'available'); assert.equal(source.reason, 'owned_minio_cache');
    row.resolve = { status: source.status, reason: source.reason, expiresAt: source.expiresAt };
    row.audio = await range(manager.src);
  }
  report.simulatedNativeAutomaticNext = 'PASS';
  phase = 'seek_and_real_progress';
  player.seek(133000); await player.saveChain;
  assert.equal(manager.currentTime, 133);
  const saved = await api.openQueue();
  assert.equal(saved.index, 1); assert.equal(saved.positionMs, 133000);
  report.realQueueRoundtrip = { index: saved.index, positionMs: saved.positionMs, result: 'PASS' };
  const oldUrl = manager.src, oldSource = lastResolutions.get(metadata[1].id);
  if (waitExpiry) {
    phase = 'waiting_actual_api_url_expiry';
    const expiry = Date.parse(oldSource.expiresAt);
    assert(Number.isFinite(expiry) && expiry - Date.now() <= 310000, 'BOUNDED_EXPIRY_REQUIRED');
    console.log(JSON.stringify({ phase, waitingSeconds: Math.ceil((expiry - Date.now() + 2000) / 1000), nativeFeishuDeviceExecuted: false }));
    await sleep(Math.max(0, expiry - Date.now() + 2000));
    report.actualApiUrlExpiry = await range(oldUrl, 403);
  } else report.actualApiUrlExpiry = 'NOT_WAITED';
  phase = 'simulated_native_error_refresh';
  const before = resolveCount;
  events.Error();
  await settled(() => resolveCount > before && player.state.status === 'playing');
  assert.equal(player.state.positionMs, 133000); assert.equal(manager.startTime, 133);
  report.simulatedNativeErrorRefresh = { apiResolveRequests: resolveCount - before, restoredPositionMs: player.state.positionMs, media: await range(manager.src) };
  assert.equal(resolveCount - before, 1);
  assert(!diag.export('service').includes(api.token));
  assert(!/https?:|owned_minio_cache|netease:/.test(diag.export('service')));
  report.diagnosticsRedaction = 'PASS';
  report.result = 'PASS';
} catch (error) {
  report.result = 'FAIL'; report.failure = { phase, code: errorCode(error) }; process.exitCode = 1;
} finally {
  if (player) { player.reset(); if (player.saveChain) await player.saveChain; }
  if (api && api.token) {
    try {
      await api.saveProgress({ queueIds: [], index: 0, positionMs: 0 });
      report.testQueueCleared = true;
    } catch (_) { report.testQueueCleared = false; }
    // Revoke the test session even if its queue cleanup was rejected.
    try { await api.logout(); report.testSessionRevoked = true; }
    catch (_) { report.testSessionRevoked = false; }
    report.testQueueClearedAndSessionRevoked = report.testQueueCleared && report.testSessionRevoked;
    if (!report.testQueueClearedAndSessionRevoked) {
      report.result = 'FAIL'; report.failure = report.failure || { phase: 'test_cleanup', code: 'CLEANUP_FAILED' };
      process.exitCode = 1;
    }
  }
  report.finishedAt = new Date().toISOString();
  await mkdir('evidence', { recursive: true });
  await writeFile('evidence/real-player-probe.json', JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify(report, null, 2));
}
