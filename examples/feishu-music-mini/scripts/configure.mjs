import { readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url), { originOf } = require('../src/lib/safe.js');
const args = process.argv.slice(2), options = {}, mediaOrigins = [];
if (args.includes('--help') || args.length === 0) {
  console.log('npm run configure -- --app-id cli_YOUR_ID --api-origin https://YOUR_TEST_HOST [--audio-origin https://YOUR_MEDIA_HOST] [--web-url https://YOUR_WEB_HOST/] [--mode fixture|service] (audio-origin may repeat)');
  process.exit(args.length ? 0 : 1);
}
assert.equal(args.length % 2, 0, 'each option needs a value');
for (let i = 0; i < args.length; i += 2) {
  assert(['--app-id','--api-origin','--audio-origin','--web-url','--mode'].includes(args[i]), 'unknown option');
  if (args[i] === '--audio-origin') {
    const media = args[i+1];
    assert.equal(originOf(media, false), media, 'audio-origin must be a bare HTTPS origin');
    mediaOrigins.push(media); continue;
  }
  assert(!Object.hasOwn(options,args[i]), 'duplicate option'); options[args[i]] = args[i+1];
}
assert(/^cli_[a-zA-Z0-9]+$/.test(options['--app-id'] || ''), 'use the nonsecret App ID from your developer console');
const origin = options['--api-origin']; assert.equal(originOf(origin, false), origin, 'api-origin must be a bare HTTPS origin');
assert(!origin.includes('.invalid'), 'replace the placeholder with an existing test origin');
const c = JSON.parse(await readFile('config.example.json', 'utf8'));
c.appId = options['--app-id']; c.apiBaseUrl = origin; c.audioOrigins = [...new Set([origin, ...mediaOrigins])]; c.mode = options['--mode'] || 'service';
assert(['fixture','service'].includes(c.mode));
if (options['--web-url']) {
  c.webPlayerUrl = options['--web-url']; assert(!/[?#]/.test(c.webPlayerUrl)); c.webOrigins = [originOf(c.webPlayerUrl, false)];
}
await writeFile('config.local.json', JSON.stringify(c,null,2) + '\n', { mode: 0o600 });
console.log(JSON.stringify({ config: 'config.local.json', mode: c.mode, apiOrigin: origin, audioOrigins: c.audioOrigins, appIdConfigured: true, webFallbackConfigured: !c.webPlayerUrl.includes('.invalid'), next: 'npm run verify' }));
