'use strict';
// All keys are scoped to a deployment and mode. No browser/NetEase cookie import.
function storage(tt, scope) {
  const prefix = 'betago_music_v1_' + scope + '_';
  return {
    get: key => { try { return tt.getStorageSync(prefix + key); } catch (_) { return undefined; } },
    set: (key, value) => tt.setStorageSync(prefix + key, value),
    remove: key => tt.removeStorageSync(prefix + key)
  };
}
module.exports = { storage };
