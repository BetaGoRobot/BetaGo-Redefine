# Independent music module

The module stores personal music data in Postgres through GORM and plain, uncached SQL. It does not use the shared management Bearer, the Bot instance tenant ID, NetEase login, QR codes, user cookies, refresh goroutines, unlock services, or cross-source matching. `source:externalID` identifies a recording. Guest identities are random; verified identities use `(application ID, actual Feishu tenant_key, verified open_id)`.

The same `internal/music.Module` runs inside `cmd/larkrobot` or in `cmd/music-server`. It is disabled in the Bot unless `MUSIC_ADDR` is set. Prefer the separate executable for an independent deployment; no Bot restart is necessary. No administration, health, model, or Bot messaging endpoints are exposed on its listener.

## Configuration and migration

Use `deploy/music.env.example` as a list of variable names. Supply actual values via a private environment/secret manager. Never commit an actual environment file. An enabled module requires an exact `MUSIC_ORIGIN` and an explicit `MUSIC_APP_ID`. Use different app boundaries for master/next applications. Configure HTTPS and `MUSIC_COOKIE_SECURE=true` in production. Keep the database and catalogue adapter on private networks.

`script/sql/20261113_music.sql` is additive and registered in the standard schema migration list. The standalone module applies the same SQL in a transaction. An advisory transaction lock serializes music schema setup. No existing Bot tables are altered. Back up the database before production migration. The migration role needs CREATE TABLE/INDEX; the runtime role needs SELECT/INSERT/UPDATE/DELETE on music tables. When runtime DDL is disallowed, run the additive migration separately and set `MUSIC_MIGRATE=false`; startup then verifies that all nine tables exist without issuing DDL. Do not grant access to unrelated Bot tables just for music.

Build with the Go version from go.mod and repository baseline:

```sh
BETAGO_CONFIG_PATH=/absolute/main-workspace/.dev/config.toml \
  go test -tags=custom_skip_vips -v ./internal/music ./cmd/music-server
BETAGO_CONFIG_PATH=/absolute/main-workspace/.dev/config.toml \
  go build -tags=custom_skip_vips -trimpath -o music-server ./cmd/music-server
```

The focused persistence tests additionally require `TEST_MUSIC_DSN` targeting an **isolated database whose name contains music_test**. They truncate only music tables there. Add `-race` for concurrency validation. The binary does not load Bot credentials or initialize Bot infrastructure.

A private Postgres 15+ database is sufficient. The music tables are `music_users`, `music_sessions`, `music_tracks`, `music_playlists`, `music_entries`, `music_likes`, `music_history`, `music_queues`, and `music_shares`. Session and recovery secrets and share tokens are random 256-bit values, SHA256 hashed at rest. Expired sessions/shares cannot authenticate; scheduled deletion of expired rows can be added to existing operational maintenance. Playlist deletion retains a tombstone. Do not remove tombstones while stale clients may remain.

## Web deployment

Build the associated YesPlayMusic music branch with Node 16 and `yarn install --frozen-lockfile --ignore-scripts && yarn build`. The static frontend talks only to same-origin `/music-api/v1` for personal data and anonymous catalogue access. Use its Nginx configuration or set `MUSIC_STATIC_DIR` on the standalone server. The Nginx configuration avoids logging URL query strings or share tokens. Never proxy this prefix to the administration API.

The frontend's Compose file builds a static Nginx image and this API separately. It requires `MUSIC_DATABASE_URL` and `MUSIC_ORIGIN`; it does not provision/restart an existing Bot. The old embedded Netease service, TLS-disable option, and unlock DNS network are removed. A retiring service worker replaces the original asset cache; personal APIs use `Cache-Control: no-store`. Docker packaging must be validated on a host with Docker access before production release.

For anonymous catalogue access, set `MUSIC_NETEASE_URL` to an independently verified anonymous Netease API. Confirm that the service itself has no global personal `MUSIC_U` and no unlock proxy enabled. The Go adapter sends no cookies and requests `unblock=false`; it honors unavailable/preview results and rejects media hosts outside official NetEase music domains. MUSIC_OWNED_MEDIA_HOST can explicitly allow one owned media proxy hostname (used only for the isolated synthetic QA preview here); it must not be set to an automatic matching service. The compatibility /song/url endpoint is tried once only after a transport/decoder error from /song/url/v1, and its media host is checked identically. No source URL is persisted. Resolve returns an expiring URL (provider lifetime capped conservatively at 5 minutes), status and reason. Browser errors trigger at most one re-resolution per selected track and restore progress after loading; persistent failure stops visibly. H5 background/lock-screen behavior remains controlled by the browser and operating system.

## Optional Feishu identity

The backend exchanges a temporary code at the official v2 token endpoint, then calls user_info with that server-held token. Missing tenant_key/open_id or a rejected exchange fails closed. Tokens are never returned to the browser or persisted. Configure the actual app ID, app secret, redirect URI, required app permissions, and the app's allowed web domain. Build the optional frontend `VUE_APP_FEISHU_APP_ID` to match. The host must provide the Feishu `tt.requestAccess` JSSDK; the button is shown only when that capability is present. External browsers remain guests. This flow has protocol tests; it requires a real approved Feishu web app and client for production verification. No claim of successful real Feishu authorization is made.

Guest and Feishu spaces are separate identities. Export guest playlists/likes and import after switching if copying is desired. The guest recovery flow restores the same guest space using a separately rotated secret; a read-only share link cannot recover or own that account. Clearing browser data without a recovery code or JSON backup loses the entry to a guest space. This is not automatic cross-device identity.

Official references: [token exchange](https://open.feishu.cn/document/authentication-management/access-token/get-user-access-token), [verified user info](https://open.feishu.cn/document/server-docs/authentication-management/login-state-management/get), [H5 login](https://open.feishu.cn/document/client-docs/h5/development-guide/step-3).

## Rollback

1. Keep the previous frontend image/build and API binary available. Restore the prior static build and stop only this independent music service, or unset MUSIC_ADDR for its next Bot deployment.
2. Leave the additive music tables intact. Rolling back code must not drop personal data, reset revisions, or revive tombstones. Do not reintroduce personal cookie initialization just to provide guest playback.
3. Restore a database snapshot only as an explicitly planned recovery; it can roll back revocations and revisions. Revoke/reissue sessions and shares after such recovery.
4. Reverting to the pre-MVP card implementation also reintroduces its old login initialization. Treat that as a separate compatibility decision, not an automatic part of music-service rollback.
