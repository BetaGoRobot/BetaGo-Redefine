'use strict';
// Intentionally never accepts raw SDK errors, HTTP bodies, titles, IDs or URLs.
const KEYS = ['index', 'positionMs', 'durationMs', 'attempt', 'status', 'code', 'visible', 'playing'];
class Diagnostics {
  constructor(now) { this.now = now || Date.now; this.started = this.now(); this.events = []; this.device = {}; }
  setDevice(info) {
    ['platform', 'system', 'model', 'version', 'SDKVersion'].forEach(k => {
      this.device[k] = String((info || {})[k] || 'unknown').replace(/[^a-zA-Z0-9 ._()+/-]/g, '').slice(0, 80);
    });
  }
  add(event, data) {
    const row = { atMs: this.now() - this.started, event: /^[a-z_]{1,40}$/.test(event) ? event : 'unknown' };
    KEYS.forEach(k => {
      const v = (data || {})[k];
      if (typeof v === 'number' && Number.isFinite(v)) row[k] = Math.round(v);
      if (typeof v === 'boolean') row[k] = v;
      if ((k === 'status' || k === 'code') && typeof v === 'string' && /^[a-zA-Z_0-9]{1,48}$/.test(v)) row[k] = v;
    });
    this.events.push(row);
    if (this.events.length > 240) this.events.shift();
  }
  export(mode) {
    return JSON.stringify({ schema: 1, mode: mode === 'fixture' ? 'FIXTURE_NOT_DEVICE_PROOF' : 'SERVICE', device: this.device, events: this.events }, null, 2);
  }
}
module.exports = { Diagnostics };
