# Explicit Kuwo catalogue sidecar

This Node 24 service adds an **explicitly selected** `kuwo:<numericID>` source to the music module. It does not log in, accept account cookies, match a NetEase ID or call third-party aggregators. Search returns title/version/artist/album/duration; free recording resolution requires public rights, the exact ID, verified HTTPS/Range, and MPEG duration matching. Restrictions and unverified audio fail closed.

The Go service remains NetEase-only unless `MUSIC_KUWO_API_URL=http://127.0.0.1:37836` is set. Existing application sessions remain independent of music source. The web source selector discovers configured options at `/music-api/v1/catalog/options`.

## Run from this checkout

```sh
node --version # 24.11+ in the 24.x series
node alternative-source.cjs --selftest
mkdir -p data
MUSIC_KUWO_CATALOGUE_PATH="$PWD/data/catalogue.json" node alternative-source.cjs
```

The service binds **127.0.0.1:37836**, with no public bind option. Node's HTTPS requests explicitly enable certificate verification. `MUSIC_KUWO_PORT` can select a different dedicated port. The bundled pure DES codec loads from `vendor/kwDES.cjs`; `MUSIC_KUWO_DES_MODULE` may point to the same audited module elsewhere, never an untrusted client path.

The catalogue file contains only verified public track metadata and the search query used to find the exact ID. Writes use an adjacent `.tmp` file and atomic rename, so the whole data directory must be writable. Audio URLs and account/session credentials are never persisted. The public detail endpoint was unreliable during validation; only records verified by official search are indexed. Preserve the catalogue across source-service restarts to reopen previously found tracks. Resolution refreshes the exact selected ID and public rights; it never trusts a caller's title or duration.

The main service can run on the same host/network namespace and use the loopback URL above. Requests to this sidecar never forward application cookies or source-account authorization.

## Container example

`compose.yml` is an isolated example, not an automatic deployment action. It uses host networking so the loopback listener can be reached by the standalone music service. Prepare a writable `data/` owned by the UID/GID selected for the container (defaults 1000:1000), then:

```sh
docker compose -f compose.yml --project-name betago-kuwo config --quiet
docker compose -f compose.yml --project-name betago-kuwo up -d
```

It mounts only this code directory read-only and its public-metadata data directory read/write. It contains no NetEase, Feishu, MinIO or personal-account secret. It does not restart the Bot, database or other source services. Avoid running it together with another listener on 37836; stop only this independent sidecar for rollback and unset `MUSIC_KUWO_API_URL` when rolling back the additional source.

## Read-only API

All routes are GET; other methods return 405. No login/account routes exist.

| Route | Response |
|---|---|
| `/health` | `{ok:true,source:"kuwo",account:false}` |
| `/search?q=...` | `{tracks:[{id,source,externalID,title,version,artist,album,picture,durationMs}]}` |
| `/tracks/<numericID>` | `{track:Track}` from the verified catalogue |
| `/resolve/<numericID>` | `{trackID,status,url?,expiresAt?,reason?,audio?}` |
| `/lyrics/<numericID>` | Same trackID, empty `lrc` and `translatedLrc` |

Successful direct resolution includes `audio.tlsVerified`, MPEG duration and resource size. `expiresAt` is a **local two-minute recheck deadline**, not a promise about an upstream signature's lifetime. Failed rights or short/unrecognized audio returns unavailable without URL. A paid/preview-only item is not converted into a complete free recording. Requests are bounded by response sizes, absolute upstream timeouts and at most four active source operations; the API process additionally bounds its own requests.

Real read-only checks (resolve output intentionally redacts the temporary URL):

```sh
curl -fsS http://127.0.0.1:37836/health
curl -fsS --get --data-urlencode 'q=小星星 纯音乐' http://127.0.0.1:37836/search
curl -fsS http://127.0.0.1:37836/tracks/629335603
curl -fsS http://127.0.0.1:37836/resolve/629335603 | node -e 'let s="";process.stdin.on("data",b=>s+=b).on("end",()=>{const {url,...safe}=JSON.parse(s);console.log(JSON.stringify({...safe,audioHost:url?new URL(url).hostname:null}))})'
```

The tested sample is **小星星 (伴奏版)**, 贝乐虎儿歌, `kuwo:629335603`: directory 75000ms, HTTPS `kw-lv.kuwo.cn`, Range206/8192 bytes, total1202674 bytes, MPEG frame duration75128ms. It is not a substitute for another song. `kuwo:5886682` 海阔天空 had restricted/preview public rights and returned unavailable. These server-side reads do not replace a real browser, mobile or listening check; current deployment evidence is kept separately.

## Go and MinIO integration

`music.KuwoProvider{BaseURL,Client}` implements the existing Provider interface and `SearchSource(ctx,"kuwo",q)`. The source router preserves literal platform-prefixed IDs for detail, resolve, lyrics and artwork. Unknown sources do not fall through to another provider.

When the independent owned cache is explicitly enabled, Kuwo uses `music/kuwo/<id>.mp3`, `catalog/kuwo/<id>.json`, `lyrics/kuwo/<id>.json`, `picture/kuwo/<id>.jpg`; NetEase retains its existing fixed layout and legacy info. Source/ID/path checks prevent equal numeric IDs colliding. Both share the module's two-writer limit. Only a stored final manifest proves persistence; configuration or source playback alone does not.

## Codec provenance and license

`vendor/kwDES.cjs` is the unchanged `src/kwDES.js` from `@unblockneteasemusic/server` **0.28.0**, [UnblockNeteaseMusic/server](https://github.com/UnblockNeteaseMusic/server). SHA-256: `fb885d12a5c40f0b784d253878173ea0805cd376fa65fe46753222ac8a06425e`. Original attribution remains in the file. Exact upstream `COPYING` and `COPYING.LESSER` are included in `vendor/`; retain them with the codec when redistributing. No shared node_modules tree, configuration, certificate, cookie or account token is bundled.
