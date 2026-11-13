'use strict';

function fault(code) { const e = new Error(code); e.code = code; return e; }
function originOf(value, local) {
  if (typeof value !== 'string' || /[\s\\]/.test(value)) throw fault('INVALID_URL');
  const m = /^(https?):\/\/([a-z0-9.-]+)(:\d+)?(?:[/?#]|$)/i.exec(value);
  if (!m || (m[1] !== 'https' && !(local && /^(localhost|127\.0\.0\.1)$/.test(m[2])))) throw fault('UNSAFE_URL');
  return m[1].toLowerCase() + '://' + m[2].toLowerCase() + (m[3] || '');
}
function allowedUrl(value, origins, local) {
  const origin = originOf(value, local);
  if (origins.indexOf(origin) < 0) throw fault('URL_ORIGIN_DENIED');
  return value;
}
function ms(value, max) { return Math.min(max || 86400000, Math.max(0, Number(value) || 0)); }
function errorCode(e) {
  const code = e && e.code;
  return typeof code === 'string' && /^[A-Z_0-9]{1,48}$/.test(code) ? code : 'OPERATION_FAILED';
}
module.exports = { fault, allowedUrl, originOf, ms, errorCode };
