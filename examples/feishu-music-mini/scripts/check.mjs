import { readdir, readFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
import path from 'node:path';
async function walk(dir) {
  let files = [];
  for (const e of await readdir(dir, { withFileTypes: true })) {
    if (['audio', 'node_modules', 'dist', '.git'].includes(e.name)) continue;
    const p = path.join(dir, e.name); files = files.concat(e.isDirectory() ? await walk(p) : [p]);
  }
  return files;
}
const files = await walk('.'); let js = 0;
for (const file of files) {
  if (/\.(js|cjs|mjs)$/.test(file)) {
    const r = spawnSync(process.execPath, ['--check', file], { encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr); js++;
  }
  if (file.endsWith('.json')) JSON.parse(await readFile(file, 'utf8'));
}
const manifest = JSON.parse(await readFile('src/app.json'));
for (const page of manifest.pages) {
  for (const ext of ['js', 'ttml', 'ttss', 'json']) await readFile('src/' + page + '.' + ext);
  const template = await readFile('src/' + page + '.ttml', 'utf8'), script = await readFile('src/' + page + '.js', 'utf8');
  for (const m of template.matchAll(/bind\w+="(\w+)"/g)) assert.match(script, new RegExp('\\b' + m[1] + '\\('), 'missing template handler ' + m[1]);
}
// XML parser validates TTML nesting with the platform namespace declared on a wrapper.
const xml = spawnSync('python3', ['-c', "import xml.etree.ElementTree as E; from pathlib import Path; [E.fromstring('<root xmlns:tt=\"urn:tt\">'+p.read_text()+'</root>') for p in Path('src').rglob('*.ttml')]; print('TTML well-formed')"], { encoding: 'utf8' });
assert.equal(xml.status, 0, xml.stderr);
console.log(JSON.stringify({ jsSyntaxFiles: js, pages: manifest.pages.length, ttml: 'well-formed', proof: 'STATIC_ONLY_NOT_FEISHU_COMPILER' }));
