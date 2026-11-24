import { mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
await mkdir('dist', { recursive: true });
await readFile('fixtures/audio/manifest.json');
for (const [name, inputs] of [
  ['betago-music-mini-source.zip', ['src','scripts','test','fixtures/LICENSE','fixtures/server.cjs','config.example.json','package.json','package-lock.json','README.md','docs','deploy','LICENSE','.gitignore']],
  ['betago-music-audio-fixtures.zip', ['fixtures/audio','fixtures/LICENSE','fixtures/server.cjs','scripts/generate-audio.py']]
]) {
  await rm('dist/' + name, { force: true });
  const r = spawnSync('zip', ['-q','-r','dist/' + name, ...inputs], { encoding: 'utf8' }); assert.equal(r.status, 0, r.stderr);
}
const artifacts = {};
for (const name of ['betago-music-mini-dev.zip','betago-music-mini-source.zip','betago-music-audio-fixtures.zip']) {
  const b = await readFile('dist/' + name); artifacts[name] = { bytes: b.length, sha256: createHash('sha256').update(b).digest('hex') };
}
await writeFile('dist/artifacts.json', JSON.stringify({ proof: 'LOCAL_ARTIFACTS_NOT_DEVICE_PROOF', artifacts }, null, 2) + '\n');
console.log(JSON.stringify(artifacts, null, 2));
