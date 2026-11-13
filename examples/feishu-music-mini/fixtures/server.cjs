'use strict';
// Test-only protocol fixture, deliberately has NO production storage/catalogue/provider.
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');
const tracks = ['a', 'b'].map(c => ({ id: 'fixture:tone-' + c, source: 'fixture', externalID: 'tone-' + c, title: '[测试音频] 合成音 ' + c.toUpperCase() + ' · 16 分钟', artist: 'BetaGo synthetic CC0', durationMs: 960000 }));
function createFixtureServer(options = {}) {
  const now = options.now || Date.now;
  const queues = new Map(), tokens = new Set();
  let lease = 0, revoked = 0, serial = 0;
  const leases = new Map();
  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, 'http://127.0.0.1');
    const json = (status, body) => { res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(body)); };
    let body = {};
    try {
      const chunks = []; let size = 0;
      for await (const c of req) { size += c.length; if (size > 65536) { json(413, { error: 'fixture body too large' }); return; } chunks.push(c); }
      if (size) body = JSON.parse(Buffer.concat(chunks));
    } catch (_) { json(400, { error: 'fixture invalid json' }); return; }
    const prefix = '/fixture/music-api/v1';
    if (url.pathname === '/fixture/health') { json(200, { fixture: true, nativeDeviceProof: false }); return; }
    if (url.pathname.startsWith('/fixture/audio/')) {
      const id = Number(url.searchParams.get('lease')), entry = leases.get(id);
      if (!entry || id <= revoked || now() > entry.expires || url.pathname !== '/fixture/audio/' + entry.file) { json(403, { error: 'fixture URL expired' }); return; }
      const file = path.join(__dirname, 'audio', entry.file);
      if (!fs.existsSync(file)) { json(503, { error: 'run npm run fixtures:generate' }); return; }
      const size = fs.statSync(file).size;
      let start = 0, end = size - 1, status = 200;
      if (req.headers.range) {
        const m = /^bytes=(\d+)-(\d*)$/.exec(req.headers.range);
        if (!m) { res.writeHead(416, { 'Content-Range': 'bytes */' + size }); res.end(); return; }
        start = Number(m[1]); end = m[2] ? Math.min(size - 1, Number(m[2])) : end; status = 206;
        if (start >= size || end < start) { res.writeHead(416, { 'Content-Range': 'bytes */' + size }); res.end(); return; }
      }
      const headers = { 'Content-Type': 'audio/wav', 'Content-Length': end - start + 1, 'Accept-Ranges': 'bytes', 'Cache-Control': 'no-store' };
      if (status === 206) headers['Content-Range'] = 'bytes ' + start + '-' + end + '/' + size;
      res.writeHead(status, headers);
      if (req.method === 'HEAD') { res.end(); return; }
      fs.createReadStream(file, { start, end }).pipe(res); return;
    }
    if (url.pathname === prefix + '/auth/guest') {
      const token = createHash('sha256').update('fixture-only-' + (++serial)).digest('hex'); tokens.add(token);
      json(200, { sessionToken: token, user: { id: 'fixture-guest', kind: 'guest' }, expiresAt: new Date(now() + 86400000).toISOString() }); return;
    }
    if (url.pathname === prefix + '/auth/recover') { json(503, { error: 'fixture recovery deliberately not implemented' }); return; }
    const token = (req.headers.authorization || '').replace(/^Bearer /, '');
    if (!tokens.has(token)) { json(401, { error: 'fixture session missing' }); return; }
    if (url.pathname === prefix + '/me') { json(200, { user: { id: 'fixture-guest', kind: 'guest' } }); return; }
    if (url.pathname === prefix + '/search') { json(200, { tracks: tracks.filter(t => t.title.includes(url.searchParams.get('q') || '')) }); return; }
    if (url.pathname === prefix + '/auth/logout') { tokens.delete(token); json(200, {}); return; }
    if (url.pathname === '/fixture/control/expire') { revoked = lease; json(200, { fixture: true, revokedThrough: revoked }); return; }
    if (url.pathname === prefix + '/tracks/details') { json(200, { tracks: tracks.filter(t => (body.ids || []).includes(t.id)) }); return; }
    const resolve = new RegExp('^' + prefix + '/tracks/([^/]+)/resolve$').exec(url.pathname);
    if (resolve) {
      const id = decodeURIComponent(resolve[1]), t = tracks.find(t => t.id === id);
      if (!t) { json(404, { error: 'fixture track unknown' }); return; }
      const expires = now() + (options.leaseMs || 60000), number = ++lease;
      const file = t.externalID + '.wav'; leases.set(number, { file, expires });
      const origin = options.publicOrigin || 'http://127.0.0.1:' + server.address().port;
      json(200, { trackID: id, status: 'available', url: origin + '/fixture/audio/' + file + '?lease=' + number, expiresAt: new Date(expires).toISOString() }); return;
    }
    const queue = new RegExp('^' + prefix + '/queue/([a-zA-Z0-9_-]+)$').exec(url.pathname);
    if (queue) {
      const key = token + ':' + queue[1];
      const current = queues.get(key) || { revision: 0, trackIDs: tracks.map(t => t.id), currentIndex: 0, positionMs: 0 };
      if (req.method === 'PUT') {
        if (body.revision !== current.revision) { json(409, { error: 'fixture stale revision' }); return; }
        if (!Array.isArray(body.trackIDs) || body.trackIDs.some(id => !tracks.some(t => t.id === id))) { json(400, { error: 'fixture track missing' }); return; }
        const q = { revision: current.revision + 1, trackIDs: body.trackIDs, currentIndex: body.currentIndex, positionMs: body.positionMs }; queues.set(key, q); json(200, q); return;
      }
      json(200, current); return;
    }
    const playlist = { id: 'fixture-dual', name: '[测试音频] 两首合成音 · 共 32 分钟', revision: 1, tracks };
    if (url.pathname === prefix + '/playlists') { json(200, { playlists: [playlist] }); return; }
    if (url.pathname === prefix + '/playlists/fixture-dual') { json(200, playlist); return; }
    json(404, { error: 'fixture route missing' });
  });
  return server;
}
if (require.main === module) {
  const port = Number(process.env.FIXTURE_PORT || 8787), host = process.env.FIXTURE_HOST || '127.0.0.1';
  createFixtureServer({ publicOrigin: process.env.FIXTURE_PUBLIC_ORIGIN, leaseMs: Number(process.env.FIXTURE_LEASE_MS || 60000) }).listen(port, host, () => {
    console.log(JSON.stringify({ fixture: true, nativeDeviceProof: false, listen: host + ':' + port, publicOriginConfigured: !!process.env.FIXTURE_PUBLIC_ORIGIN }));
  });
}
module.exports = { createFixtureServer, tracks };
