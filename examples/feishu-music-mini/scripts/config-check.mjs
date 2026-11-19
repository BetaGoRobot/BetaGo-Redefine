import { readFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
let c = JSON.parse(await readFile(process.env.MINI_CONFIG || 'config.example.json', 'utf8'));
let manifest;
try { manifest = JSON.parse(await readFile('fixtures/audio/manifest.json')); } catch (_) {}
const opdev = spawnSync('which', ['opdev'], { encoding: 'utf8' });
console.log(JSON.stringify({
  runAs: spawnSync('id', ['-un'], { encoding: 'utf8' }).stdout.trim(),
  appIdConfigured: /^cli_[a-zA-Z0-9]+$/.test(c.appId),
  apiOriginConfigured: !c.apiBaseUrl.includes('.invalid'),
  httpsOrigin: c.apiBaseUrl.startsWith('https://'),
  fixtureAudioPrepared: !!manifest && manifest.tracks.length === 2 && manifest.tracks.every(t => t.durationMs > 600000),
  officialCLIInstalled: opdev.status === 0,
  existingAppGadgetEnabled: 'UNVERIFIED_REQUIRES_APP_OWNER',
  developerLoginAvailable: 'NOT_DISCOVERED',
  actualFeishuDevices: 'NOT_AVAILABLE',
  previewCreated: false, published: false
}, null, 2));
