# Authelia Selective WebUI Authentication Design

Date: 2026-07-31

## Goal

Keep the BetaGo WebUI and its non-sensitive operational reads anonymously
available while requiring an existing Authelia `one_factor` session for:

- every mutating API request;
- every API response that contains identities, evaluation content, management
  configuration, or rollout state;
- entering the WebUI management mode.

Bot bearer tokens and other upstream credentials must never be serialized into
browser-readable JavaScript, DOM, local storage, API responses, or error
messages.

## Security boundaries

### Public

- WebUI static assets and SPA routes;
- a sanitized `/config.js`;
- `GET /api/health`;
- chat summaries, basic chat details, token/message statistics, and
  non-identity aggregate insights;
- CORS preflight requests.

### Authenticated with Authelia `one_factor`

- all `PUT`, `POST`, `PATCH`, and `DELETE` API requests;
- evaluation list, detail, and judgment APIs;
- chat member data;
- configuration values and feature state;
- per-chat and batch Agentic rollout state;
- identity-bearing insights such as top senders and top mentions;
- the management-session probe and login bridge.

### Private service-to-service

Each Bot keeps its existing `[webui_config].auth_token`. Caddy injects the
matching bearer token when proxying to that Bot. The browser never receives or
sends this credential.

The Bot backend independently enforces bearer authentication for all writes and
the sensitive GET routes. This preserves defense in depth if a request reaches
the Bot without traversing Traefik.

## Request routing

Traefik exposes two routers for the same WebUI host and service:

1. A high-priority protected router matches:
   - write HTTP methods under `/api` and `/bot/<id>/api`;
   - the explicit sensitive GET path set;
   - `/auth/session` and `/auth/login`.
   It applies the configured Authelia ForwardAuth middleware.
2. A lower-priority public router matches the host and applies no authentication
   middleware.

This keeps public reads available when Authelia is unavailable while ensuring
protected requests cannot fall through to the public router.

`OPTIONS` remains public. Traefik supplies trusted identity headers only on the
protected router; the application does not use browser-supplied identity
headers as authorization.

## Credential handling

`WEBUI_AUTH_MODE=authelia` enables secure credential handling.

In this mode the container entrypoint parses `VITE_BOTS` once and creates:

- a browser runtime configuration containing public Bot metadata only;
- a private Caddy upstream file containing the Bot ID, internal upstream URL,
  and bearer token.

The public projection uses an allowlist of fields rather than a denylist:
`id`, `name`, `remark`, and `color`. Internal `baseURL`, `token`, `api_key`,
`secret`, `password`, and unknown future fields are excluded.

The default single-Bot upstream reads `BACKEND_AUTH_TOKEN`. Multi-Bot upstreams
read their individual `token` fields from `VITE_BOTS`. These values are written
only to the private Caddy configuration inside the container.

Legacy mode retains the current browser-token behavior for compatibility.
Authelia mode removes historic Bot tokens from local storage during startup and
does not render or accept token fields in the Bot picker.

## Frontend experience

Anonymous visitors see the dashboard, chat list, and public analytics normally.
The application header shows `只读模式`.

Sensitive tabs do not issue protected requests until the user is authenticated.
They render a lock panel with a `登录后管理` action.

The action opens `/auth/login` in a popup created directly from the user click.
The parent window polls `/auth/session`. When Authelia confirms the session, the
popup closes and the current view changes to `管理模式` without losing its
route, filters, or drafts.

If the popup is blocked, the UI offers a new-tab/same-window fallback. Before a
same-window fallback it stores only non-secret navigation and draft state in
`sessionStorage`, restores it after returning, and deletes the stored record.

A write is never automatically replayed after authentication. The user reviews
and confirms it again. A session expiring during editing preserves the draft,
shows the lock panel, and treats the failed request as uncommitted.

## Session endpoints

Caddy serves two bridge paths:

- `GET /auth/session`: after Traefik/Authelia approval, returns a minimal
  no-store JSON response indicating that management mode is available.
- `GET /auth/login`: after approval, returns a small same-origin page that
  notifies the opener and closes itself. Without a session, ForwardAuth sends
  the user through the Authelia portal first.

Neither response contains identity claims, tokens, or upstream configuration.

## Failure behavior

- Anonymous protected API calls receive a consistent `401` or `403`; the
  frontend converts this into the locked management state.
- Authentication cancellation leaves the current page and draft untouched.
- Authelia unavailability affects protected operations only; public reads keep
  working.
- Missing upstream bearer configuration makes protected Bot requests fail
  closed at the Bot backend.
- Error bodies and logs never echo authorization headers or credential values.

## Deployment and compatibility

Secure mode is opt-in:

```yaml
WEBUI_AUTH_MODE: authelia
AUTHELIA_MIDDLEWARE: authelia@docker
BACKEND_AUTH_TOKEN: <same value as the default Bot webui_config.auth_token>
```

The protected Traefik router references the operator-provided middleware name.
The existing public router remains unchanged except for an explicit lower
priority.

The Bot backend port is not published by the production compose example. Local
debugging can explicitly opt into a loopback-only port mapping.

Legacy deployments that do not set `WEBUI_AUTH_MODE=authelia` continue to use
their current authentication behavior.

## Verification

Automated tests must prove:

- secure-mode `/config.js` cannot contain sentinel token, API key, password,
  secret, internal URL, or unknown fields;
- private Caddy routes contain the correct upstream and bearer injection without
  logging the token;
- public routes remain unauthenticated;
- every protected path/method is covered by the higher-priority router;
- backend sensitive GET and write requests reject missing/invalid bearer tokens;
- frontend secure mode purges legacy stored tokens and never sends an
  `Authorization` header;
- protected tabs do not load before authentication;
- expired sessions preserve drafts and do not replay writes;
- the complete Go and WebUI test suites and production WebUI build pass.

Manual browser verification covers anonymous public reads, locked sensitive
tabs, popup login, management-mode transition, session expiry, and mobile
layout.
