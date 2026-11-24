import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import assert from 'node:assert/strict';
const report = JSON.parse(await readFile('dist/build-report.json', 'utf8'));
const listing = spawnSync('unzip', ['-Z1','dist/betago-music-mini-dev.zip'], { encoding: 'utf8' });
assert.equal(listing.status, 0, listing.stderr);
const entries = listing.stdout.trim().split('\n').filter(f => !f.endsWith('/'));
const expected = Object.keys(report.hashes).concat('build-report.json').sort();
assert.deepEqual(entries.slice().sort(), expected, 'package contents must exactly match manifest');
let requires = 0;
for (const [file, hash] of Object.entries(report.hashes)) {
  const r = spawnSync('unzip', ['-p','dist/betago-music-mini-dev.zip',file]);
  assert.equal(r.status, 0); assert.equal(createHash('sha256').update(r.stdout).digest('hex'), hash);
  if (!file.endsWith('.js')) continue;
  for (const m of r.stdout.toString().matchAll(/require\(['"]([^'"]+)['"]\)/g)) {
    assert(m[1].startsWith('.'), 'no Node runtime dependency in gadget');
    const target = path.posix.normalize(path.posix.join(path.posix.dirname(file), m[1]));
    assert(target.startsWith('gadget/')); assert(target in report.hashes, 'missing module ' + target); requires++;
  }
}
console.log(JSON.stringify({ zipContents: 'PASS', sha256Manifest: 'PASS', gadgetModuleClosure: 'PASS', relativeRequires: requires, officialCompiler: 'NOT_RUN', realDevice: 'NOT_RUN' }));
