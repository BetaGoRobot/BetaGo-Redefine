# BetaGo Music API v1 — implemented contract (2026-11-13)

Base: `/music-api/v1`, JSON, same origin. Times RFC3339 UTC; durationMs and positionMs milliseconds. No NetEase login, QR, owner_id or personal cookie. Unknown JSON fields rejected on writes. Errors `{ "error": "..." }`, 400 invalid, 401 session expired, 404 inaccessible/deleted/revoked, 409 stale revision, 503 provider unavailable.

## Identity
- `POST /auth/guest {}` → `{user:{id,kind:"guest"}, expiresAt}` + HttpOnly SameSite=Lax music_session cookie (30d). Repeated call with valid session reuses it.
- Non-browser clients: `POST /auth/guest {"transport":"bearer"}` → also `sessionToken`; keep in platform secure storage, send `Authorization: Bearer <token>`. Never put browser cookies or tokens in URLs. All credentials SHA256 hashed server-side.
- `GET /me` → `{user:{id,kind}, expiresAt}`. `POST /auth/logout {}` revokes current session.
- `POST /auth/recovery {}` → `{recoveryCode}` rotates a separate high entropy recovery secret (shown once). `POST /auth/recover {recoveryCode,transport?}` → own session. Recovery restores the same guest library on another device; keep secret, rotate to invalidate old code. JSON export/import instead copies a library. Guest browsing does NOT provide automatic private cross-device identity.
- `POST /auth/feishu/exchange {code,transport?}` → own session only after backend code exchange and `/authen/v1/user_info` verification. Optional deployment configuration; unavailable → 503, guest remains usable. Server derives appID, actual tenant_key and subject, never trusts URL open_id. Applications and real tenants are separate boundaries.

## Tracks / sources
Stable ID is a string `source:externalID`, e.g. `netease:186016`; never song title or hardcoded numeric placeholder. URL-encode ID as path component. Metadata `{id,source,externalID,title,artist,album,picture,durationMs}`. No temporary playback URLs in stored metadata/export.
- `GET /search?q=...` → `{tracks:[Track]}` anonymous adapter, explicit upstream failure.
- `POST /tracks/details {ids:["netease:186016"]}` → `{tracks:[Track]}`.
- `GET /tracks/{id}` → Track.
- `GET /tracks/{id}/lyrics` → `{trackID, lrc, translatedLrc}` asynchronously per track. Client discards stale results on track change.
- `POST /tracks/{id}/resolve {}` → `{trackID,status:"available"|"preview"|"unavailable",url?,expiresAt?,reason?}`. Fresh URL every call, no account fallback/cross-source substitution. expiresAt is provider expiry or conservative 5 min upper bound. Preview must be labelled; unavailable has no URL. On media error/expiry refresh ONCE per selected track, restore progress after load, then stop with visible reason; never infinite retry or silent skip. Autoplay rejection requires explicit user action.
- Legacy `?target=<full descriptor URL>` handled by client asynchronous fetch. If descriptor has TrackID/Source/ExternalID use it; old `info/<numericID>.json` infers `netease:<numericID>`. Other descriptors use `legacy:<SHA256 descriptor URL>` and temporary in-memory URLs only; expired legacy descriptor requires reopening a fresh card.

## Library
- `GET /playlists` → `{playlists:[{id,name,revision,tracks:[Track]}]}`.
- `POST /playlists {name,trackIDs?:[]}` → Playlist.
- `GET /playlists/{id}` → Playlist.
- `PUT /playlists/{id} {name,trackIDs:[],revision}` → Playlist. Complete ordered list, deduplicates stable IDs. Transaction + compare-and-swap, stale 409. `DELETE /playlists/{id} {revision}` tombstones; no upsert resurrection.
- `GET /likes` → `{tracks:[Track]}`; `PUT /likes/{trackID} {}`; `DELETE /likes/{trackID}`.
- `GET /history` → `{history:[{track:Track,positionMs,playedAt}]}` (most recent 200); `POST /history {trackID,positionMs}`; `DELETE /history`.

## Device queue
`GET /queue/{deviceID}` → `{revision:0,trackIDs:[],currentIndex:0,positionMs:0}` for new device.
`PUT /queue/{deviceID} {revision,trackIDs:[],currentIndex,positionMs}` → updated snapshot with revision+1. Allows duplicate tracks (position identifies occurrence). Max 1000 items. CAS 409, never blindly retry overwrite. Queue owner from own session; deviceID (random UUID persisted locally) is NOT a credential. Refresh restores paused at position and re-resolves source; no audio URL in queue. Mini-program creates its own bearer session/device or deliberately uses recovery flow, never imports web cookie.

## Sharing / transfer
- `POST /shares {kind:"playlist"|"track",targetID}` → `{id,token}`. Token shown once, stored hashed. Default 30d readonly. Owner can list `GET /shares` (no tokens), revoke `DELETE /shares/{id}`.
- `GET /shared/{token}` → versioned export subset `{format:"betago-music",version:1,playlists:[{name,tracks:[Track]}],likes:[]}`. No owner/session/history/queue. Current playlist state until deletion/revoke/expiry; 404 afterward. Single track uses one-item playlist. Public read only, no mutation capability.
- `POST /shared/{token}/copy {}` authenticated → new playlist owned by caller.
- `GET /export` → `{format:"betago-music",version:1,playlists:[{name,tracks:[Track]}],likes:[Track]}`. No cookie/recovery/session secret/audio URL.
- `POST /import <same export object>` → `{imported:<playlist count>}`. Transactional; creates copies, never accepts owner IDs or overwrites existing playlists. Max 1000 tracks per playlist / 50 playlists / 2 MiB. Source IDs validated; untrusted metadata cannot overwrite canonical shared catalogue.

Preview address and actual verified source/Feishu support recorded separately in acceptance report. This contract belongs to betago-music-web; mini task may read, must not edit it.

Implementation addition: `POST /tracks/register {track:Track}` accepts metadata only for legacy descriptors and verifies `id === source + ':' + externalID`; URLs/lyricsURL/owner fields are not accepted. First catalogue record wins, so imports do not overwrite existing shared metadata. New card descriptors include TrackID/Source/ExternalID. During a provider 503 only, a still-valid old descriptor may play its exact in-memory URL; an explicit provider restriction/unavailable result never falls back. The same finite retry budget applies.

Anonymous NetEase resolution compatibility: if `/song/url/v1` fails at the transport/decoder level, the adapter tries `/song/url` once for the same external ID at 128 kbps. A valid restricted/unavailable/preview v1 response is final and never falls through. Both endpoints receive no cookies and `unblock=false`; client recovery remains limited to one re-resolution.

Media provenance: the anonymous NetEase adapter rejects non-NetEase audio hosts with `status:"unavailable", reason:"source_mismatch: ..."`, even when an upstream API returns the requested numerical ID. This prevents a deployed unblock/matching proxy from silently substituting a different source despite `unblock=false`. An operator may configure one exact owned proxy hostname; the preview allows its own synthetic audio host only. No third-party music host is permitted by default.
