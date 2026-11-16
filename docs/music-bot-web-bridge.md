# Bot music web entry and requested recommendations

The existing `music_search` tool and `/music` command remain the entry points.
No scheduler, unsolicited recommendation task or new messaging service is added.
This change is prepared source code; it does not configure or restart a live Bot.

## Explicit configuration

| Variable | Behavior |
| --- | --- |
| `MUSIC_WEB_ORIGIN` | Exact HTTPS origin, including the public port. No path, credentials, query or fragment. Enables browser links. |
| `MUSIC_WEB_APP_ID` | Operator-controlled Feishu web app ID. Enables AppLink and scopes the trusted incoming event identity. |
| `MUSIC_BOT_DATABASE_URL` | Explicit connection to the personal music database. Never falls back to the Bot global database or web session configuration. |

Delivery example for the existing web deployment: origin
`https://music.kmhomelab.cn:2443`, app ID `cli_a570c2871fe3500e`.
These values are not hardcoded into the URL helper or automatically applied to
the shared Bot. Keep database credentials in private deployment configuration.

With no web origin, existing descriptor `target` and player URLs remain intact.
With the origin configured, song links carry the stable `trackID` only. Search
cards include an encoded `q` link. Both cards expose an independent browser URL;
Feishu AppLink uses the configured app ID, `mode=appCenter`, and the complete
browser URL encoded into `lk_target_url`. Browser query values are encoded
first; the complete URL is then encoded as the outer parameter. The default opens in Feishu Workplace/navigation tabs; `window` explicitly
requests a separate window and is not the primary card behavior. No query or
fragment is placed in the protocol's `path` parameter.
No open ID, session cookie, OAuth code, owner ID or administrator token is added
to a link. See the [official H5 AppLink protocol](https://open.feishu.cn/document/common-capabilities/applink-protocol/supported-protocol/open-an-h5-app.md).
Unit checks prove URL structure and encoding; real Feishu client navigation and
the destination application's published URL configuration still need acceptance.

## Requested recommendation flow

```text
/music --type=recommend
music_search {"type":"recommend"}
```

Only the original SDK-verified event is an identity source: its header AppID
must match `MUSIC_WEB_APP_ID`; sender type must be `user`, and sender tenant key
and open ID must both exist. The sender tenant is not replaced by the header
tenant, Bot tenant configuration, Bot open ID, CLI metadata or URL parameters.
Synthetic events without this complete identity only receive the web entry.

The music database connection uses a separate lazy pool (maximum two open and
one idle connection), silent SQL logging, PostgreSQL session read-only mode,
two-second connect timeout and bounded statement/overall request timeouts.
It calls `music.RecommendForIdentity` with no provider, so candidates come from
the local canonical catalogue and no music/account source requests are made.
The shared core resolves the exact app/tenant/subject account and scopes its
interest reads to that owner. No account creation or migration is performed.

Successful output is a text bubble containing at most five songs, reasons and
stable web links. Missing connection configuration, identity, linked music
account or query success results in a generic web entry, without claiming a
personalized result. A missing web configuration is stated explicitly.
No reply is sent outside the current requested message.

## Production connection gap

The current personal music PostgreSQL listener is bound to host loopback
`127.0.0.1:35437`; an existing shared Bot container does not automatically reach
that address. This source change deliberately does not broaden PostgreSQL
listening, change the shared Bot network or inject a database/session secret.
Before enabling personalized reads in a future release, provide an explicitly
approved reachable read-only connection to the correct `music_preview` database.
Until then, configure only web/app links and retain the honest web-entry fallback.

## Validation and rollout boundary

Tests cover configured/legacy URL behavior, nested Unicode query encoding,
unsafe origins/track IDs, both card entry points, recommendation argument
parsing, trusted identity denial, no-account fallback, read-only PG settings and
one current-message text reply using a local test replacement. Tests do not
send real Feishu messages or query a live music/database account.

Use the repository's Go 1.26.8 wrapper and `custom_skip_vips` baseline. The Bot
source is intended for normal code review and a later explicit Bot release;
rebuilding the independent music web API does not update the shared Bot binary.
