const test = require('node:test'), assert = require('node:assert/strict');
const { mkdtemp, mkdir, copyFile, readFile, rm } = require('node:fs/promises');
const { tmpdir } = require('node:os'), path = require('node:path');
const { spawnSync } = require('node:child_process');
const { allowedUrl } = require('../src/lib/safe.js');

const apiOrigin = 'https://music-mini.kmhomelab.cn:2443';
const mediaOrigin = 'https://music.kmhomelab.cn:2443';
async function sandbox(t) {
  const dir = await mkdtemp(path.join(tmpdir(), 'music-mini-configure-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  for (const relative of ['scripts/configure.mjs', 'src/lib/safe.js', 'config.example.json']) {
    const target = path.join(dir, relative);
    await mkdir(path.dirname(target), { recursive: true });
    await copyFile(path.join(__dirname, '..', relative), target);
  }
  return dir;
}
function configure(dir, extra) {
  return spawnSync(process.execPath, ['scripts/configure.mjs', '--app-id', 'cli_configureTest123', '--api-origin', apiOrigin, ...extra], { cwd: dir, encoding: 'utf8' });
}

test('CONFIGURE: distinct explicit media origins survive CLI generation and enforce exact audio trust', async t => {
  const dir = await sandbox(t);
  const result = configure(dir, ['--audio-origin', mediaOrigin, '--audio-origin', apiOrigin, '--audio-origin', mediaOrigin]);
  assert.equal(result.status, 0, result.stderr);
  const config = JSON.parse(await readFile(path.join(dir, 'config.local.json'), 'utf8'));
  assert.equal(config.apiBaseUrl, apiOrigin);
  assert.deepEqual(config.audioOrigins, [apiOrigin, mediaOrigin]);
  assert.equal(config.mode, 'service'); assert.equal(config.authMode, 'anonymous');
  assert.equal(config.allowLocalhost, false);
  const mediaUrl = mediaOrigin + '/cloudmusic/music/109616.mp3';
  assert.equal(allowedUrl(mediaUrl, config.audioOrigins, false), mediaUrl);
  for (const denied of [
    'https://music.kmhomelab.cn.evil.test:2443/cloudmusic/music/109616.mp3',
    'https://unlisted.example.test/cloudmusic/music/109616.mp3',
    'https://music.kmhomelab.cn:2444/cloudmusic/music/109616.mp3',
    'http://music.kmhomelab.cn:2443/cloudmusic/music/109616.mp3'
  ]) assert.throws(() => allowedUrl(denied, config.audioOrigins, false), /URL_ORIGIN_DENIED|UNSAFE_URL/);
});

test('CONFIGURE: extra audio origins must be bare HTTPS origins without paths or credentials', async t => {
  const dir = await sandbox(t);
  for (const invalid of [
    mediaOrigin + '/cloudmusic', mediaOrigin + '/', mediaOrigin + '?token=test', mediaOrigin + '#fragment',
    'http://music.kmhomelab.cn:2443', 'https://test-user:test-password@music.kmhomelab.cn:2443',
    'https://*.kmhomelab.cn:2443'
  ]) {
    const result = configure(dir, ['--audio-origin', invalid]);
    assert.notEqual(result.status, 0, 'must reject invalid audio origin: ' + invalid);
    await assert.rejects(readFile(path.join(dir, 'config.local.json'), 'utf8'), { code: 'ENOENT' });
  }
});
