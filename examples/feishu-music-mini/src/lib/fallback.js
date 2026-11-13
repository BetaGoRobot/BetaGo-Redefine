'use strict';
const { fault, allowedUrl } = require('./safe.js');
function openWeb(tt, config) {
  const url = allowedUrl(config.webPlayerUrl, config.webOrigins, false);
  // Fixed configuration URL only: no tokens, arbitrary schema, redirect or track URLs.
  if (/[?#]/.test(url)) return Promise.reject(fault('WEB_URL_QUERY_DENIED'));
  return new Promise((resolve, reject) => {
    if (typeof tt.openSchema !== 'function') { reject(fault('OPEN_SCHEMA_UNAVAILABLE')); return; }
    tt.openSchema({ schema: url, external: true, success: resolve, fail: () => reject(fault('WEB_OPEN_FAILED')) });
  });
}
// Future registered providers implement canOpen/open. No unknown schemes enabled.
const externalPlayers = [];
module.exports = { openWeb, externalPlayers };
