import { cp, mkdir, readFile, readdir, writeFile, rm } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url), { originOf, allowedUrl } = require('../src/lib/safe.js');
let configFile = process.env.MINI_CONFIG || 'config.local.json', example = false;
let raw;
try { raw = await readFile(configFile, 'utf8'); } catch (e) {
  if (process.env.MINI_CONFIG || e.code !== 'ENOENT') throw e;
  configFile = 'config.example.json'; raw = await readFile(configFile, 'utf8'); example = true;
}
const c = JSON.parse(raw);
const keys = ['appId','mode','apiBaseUrl','audioOrigins','webPlayerUrl','webOrigins','authMode','feishuExchangePath','allowLocalhost','requestTimeoutMs'];
assert(Object.keys(c).every(k => keys.includes(k)), 'unknown config fields (secrets and identities forbidden)');
assert(['fixture','service'].includes(c.mode)); assert(['anonymous','feishu'].includes(c.authMode));
assert(!c.appId || /^cli_[a-zA-Z0-9]+$/.test(c.appId), 'invalid nonsecret appId');
assert(!/[?#]/.test(c.apiBaseUrl)); originOf(c.apiBaseUrl, c.allowLocalhost);
assert(Array.isArray(c.audioOrigins) && c.audioOrigins.length && c.audioOrigins.every(x => originOf(x, c.allowLocalhost) === x));
assert(Array.isArray(c.webOrigins) && c.webOrigins.every(x => originOf(x, false) === x));
allowedUrl(c.webPlayerUrl, c.webOrigins, false); assert(!/[?#]/.test(c.webPlayerUrl));
if (c.mode === 'service') assert(!c.allowLocalhost, 'service must use HTTPS');
if (c.feishuExchangePath) assert(/^\/[a-zA-Z0-9_/-]+$/.test(c.feishuExchangePath) && !c.feishuExchangePath.includes('..'));
assert(c.requestTimeoutMs >= 1000 && c.requestTimeoutMs <= 30000);
await rm('dist/gadget', { recursive: true, force: true });
await rm('dist/betago-music-mini-dev.zip', { force: true });
await mkdir('dist/gadget', { recursive: true });
await cp('src', 'dist/gadget', { recursive: true });
await writeFile('dist/gadget/config.js', "'use strict';\nmodule.exports = " + JSON.stringify(c, null, 2) + ';\n');
const project = { appid: c.appId, projectname: 'BetaGo Background Audio Prototype', miniprogramRoot: './gadget/', setting: { es6: true, minified: true } };
await writeFile('dist/project.config.json', JSON.stringify(project, null, 2) + '\n');
const warnings = [];
if (!c.appId) warnings.push('APP_ID_NOT_CONFIGURED');
if (/\.invalid/.test(c.apiBaseUrl)) warnings.push('TEST_SERVICE_ORIGIN_NOT_CONFIGURED');
if (c.allowLocalhost) warnings.push('LOCALHOST_FOR_NODE_TEST_ONLY');
const hashes = {};
async function hashDir(dir) { for (const e of await readdir(dir, { withFileTypes: true })) {
  const file = dir + '/' + e.name;
  if (e.isDirectory()) await hashDir(file); else hashes[file.slice(5)] = createHash('sha256').update(await readFile(file)).digest('hex');
} }
await hashDir('dist/gadget');
hashes['project.config.json'] = createHash('sha256').update(await readFile('dist/project.config.json')).digest('hex');
const report = { artifact: 'IMPORTABLE_DEVELOPMENT_SOURCE_PACKAGE', officialCompilerExecuted: false, realDeviceExecuted: false, config: example ? 'example' : 'local', mode: c.mode, warnings, hashes };
await writeFile('dist/build-report.json', JSON.stringify(report, null, 2) + '\n');
const zipped = spawnSync('zip', ['-q', '-r', 'betago-music-mini-dev.zip', 'gadget', 'project.config.json', 'build-report.json'], { cwd: 'dist', encoding: 'utf8' });
assert.equal(zipped.status, 0, zipped.stderr);
console.log(JSON.stringify({ package: 'dist/betago-music-mini-dev.zip', files: Object.keys(hashes).length, warnings, officialCompilerExecuted: false }));
