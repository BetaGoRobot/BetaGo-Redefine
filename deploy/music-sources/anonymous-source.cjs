// Local read-only catalogue adapter. Anonymous by default; an explicit server
// Cookie header/body can use the operator's own account without login modules.
const fs = require('fs');
const path = require('path');
const root = process.cwd();
const candidates = [process.env.NETEASE_API_ROOT,
  path.join(root, '.runtime/anonymous-source-snapshot/node_modules/NeteaseCloudMusicApi'),
  path.join(root, '.runtime/legacy-node_modules/NeteaseCloudMusicApi')].filter(Boolean);
const apiRoot = candidates.find(dir => fs.existsSync(path.join(dir, 'server.js')));
if (!apiRoot) throw new Error('Isolated anonymous catalogue dependency snapshot is missing');
const tmp = process.env.TMPDIR || path.join(root, '.runtime/anon-tmp');
fs.mkdirSync(tmp, { recursive: true });
const anonymousToken = path.join(tmp, 'anonymous_token');
if (!fs.existsSync(anonymousToken)) fs.writeFileSync(anonymousToken, '', { mode: 0o600 });
// The upstream middleware keys by URL + header cookies but ignores POST bodies.
// It can return another track/account's response and cache temporary media URLs.
// Our API owns canonical metadata; source responses must always be fresh.
require(path.join(apiRoot, 'util/apicache')).options({ enabled: false });
const allowed = ['cloudsearch', 'song_detail', 'song_url_v1', 'song_url', 'lyric', 'playlist_detail'];
const moduleDefs = allowed.map(name => ({ route: '/' + name.replaceAll('_', '/'), module: require(path.join(apiRoot, 'module', name)) }));
require(path.join(apiRoot, 'server')).serveNcmApi({ host: '127.0.0.1', port: 37833, checkVersion: false, moduleDefs });
