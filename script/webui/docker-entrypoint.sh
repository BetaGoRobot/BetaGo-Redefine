#!/bin/sh
# Render the public browser config and private Caddy upstream config at runtime.
set -eu

CONFIG_DIR="${CONFIG_DIR:-/usr/share/caddy}"
CONFIG_FILE="${CONFIG_DIR}/config.js"
BOTS_CADDY_FILE="${BOTS_CADDY_FILE:-/etc/caddy/bots.caddy}"
AUTH_GATE_CADDY_FILE="${AUTH_GATE_CADDY_FILE:-/etc/caddy/auth-gate.caddy}"
WEBUI_AUTH_MODE_VAL="${WEBUI_AUTH_MODE:-legacy}"
BACKEND_URL_VAL="${BACKEND_URL:-http://larkrobot:8090}"
BACKEND_AUTH_TOKEN_VAL="${BACKEND_AUTH_TOKEN:-}"
VITE_BOTS_VAL="${VITE_BOTS:-}"
VITE_API_BASE_VAL="${VITE_API_BASE:-}"

encode_quoted_string() {
	if command -v jq >/dev/null 2>&1; then
		printf '%s' "$1" | jq -Rs .
	else
		printf '"%s"' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e ':a;N;$!ba;s/\n/\\n/g')"
	fi
}

if [ "$WEBUI_AUTH_MODE_VAL" = "authelia" ]; then
	if [ -n "$VITE_BOTS_VAL" ] && command -v jq >/dev/null 2>&1; then
		# Browser data is an allowlist. Unknown future fields are private by
		# default, so adding a new credential cannot silently expose it.
		BOTS_JS=$(printf '%s' "$VITE_BOTS_VAL" | jq -c '
			try fromjson? // .
			| if type == "array" then . else [] end
			| map(
				select(type == "object" and (.id | type == "string"))
				| {
					id,
					name: (.name // .id),
					remark,
					color
				}
				| with_entries(select(.value != null))
			)
		')
	else
		BOTS_JS='[]'
	fi
else
	# Compatibility mode preserves the historical browser-owned bearer flow.
	BOTS_JS=$(encode_quoted_string "$VITE_BOTS_VAL")
fi

API_BASE_JS=$(encode_quoted_string "$VITE_API_BASE_VAL")
AUTH_MODE_JS=$(encode_quoted_string "$WEBUI_AUTH_MODE_VAL")

mkdir -p "$CONFIG_DIR"
cat >"$CONFIG_FILE" <<EOF
// 由容器 entrypoint 在启动时根据环境变量渲染，请勿手工修改。
// 字段定义见 webui/src/env.d.ts:BetaGoRuntimeConfig。
window.__BETAGO_CONFIG__ = {
  bots: ${BOTS_JS},
  apiBase: ${API_BASE_JS},
  authMode: ${AUTH_MODE_JS},
  sessionPath: "/auth/session",
  loginPath: "/auth/login",
};
EOF

mkdir -p "$(dirname "$BOTS_CADDY_FILE")"
mkdir -p "$(dirname "$AUTH_GATE_CADDY_FILE")"
: >"$BOTS_CADDY_FILE"
: >"$AUTH_GATE_CADDY_FILE"

append_reverse_proxy() {
	route_path=$1
	strip_prefix=$2
	upstream=$3
	token=$4

	upstream_quoted=$(encode_quoted_string "$upstream")
	cat >>"$BOTS_CADDY_FILE" <<EOF
handle ${route_path} {
EOF
	if [ -n "$strip_prefix" ]; then
		printf '\turi strip_prefix %s\n' "$strip_prefix" >>"$BOTS_CADDY_FILE"
	fi
	if [ -n "$token" ]; then
		auth_header_quoted=$(encode_quoted_string "Bearer $token")
		cat >>"$BOTS_CADDY_FILE" <<EOF
	reverse_proxy ${upstream_quoted} {
		header_up Authorization ${auth_header_quoted}
	}
EOF
	else
		cat >>"$BOTS_CADDY_FILE" <<EOF
	reverse_proxy ${upstream_quoted}
EOF
	fi
	cat >>"$BOTS_CADDY_FILE" <<'EOF'
}
EOF
}

if [ "$WEBUI_AUTH_MODE_VAL" = "authelia" ]; then
	append_reverse_proxy \
		"/api/*" \
		"" \
		"$BACKEND_URL_VAL" \
		"$BACKEND_AUTH_TOKEN_VAL"

	cat >"$AUTH_GATE_CADDY_FILE" <<'CADDY'
# The public Traefik router strips caller-supplied identity headers. Authelia
# restores them only on the protected router. Missing middleware therefore
# fails closed here instead of falling through to the public route.
@unauthenticatedWrite {
	method POST PUT PATCH DELETE
	path /api/* /bot/*/api/*
	not header Remote-User *
}
header @unauthenticatedWrite Content-Type "application/json"
header @unauthenticatedWrite Cache-Control "no-store"
respond @unauthenticatedWrite `{"error":"authentication_required"}` 401

@unauthenticatedSensitiveRead {
	method GET
	path_regexp sensitive ^/(bot/[^/]+/)?api/(evaluations(/.*)?|agentic-rollouts(/.*)?|chats/[^/]+/(members|configs|features|agentic-rollout)(/.*)?|chats/[^/]+/insights/(top_senders|top_mentions)(/.*)?)$
	not header Remote-User *
}
header @unauthenticatedSensitiveRead Content-Type "application/json"
header @unauthenticatedSensitiveRead Cache-Control "no-store"
respond @unauthenticatedSensitiveRead `{"error":"authentication_required"}` 401

@unauthenticatedAuthBridge {
	method GET
	path /auth/session /auth/login
	not header Remote-User *
}
header @unauthenticatedAuthBridge Content-Type "application/json"
header @unauthenticatedAuthBridge Cache-Control "no-store"
respond @unauthenticatedAuthBridge `{"error":"authentication_required"}` 401

@authSession path /auth/session
header @authSession Content-Type "application/json"
header @authSession Cache-Control "no-store"
respond @authSession `{"authenticated":true}` 200

@authLogin path /auth/login
header @authLogin Content-Type "text/html; charset=utf-8"
header @authLogin Cache-Control "no-store"
respond @authLogin `<!doctype html><meta charset="utf-8"><title>BetaGo 管理模式</title><script>if(window.opener){window.opener.postMessage("betago:auth-complete",window.location.origin)}window.close()</script><p>登录成功，可以关闭此窗口。</p>` 200
CADDY
else
	append_reverse_proxy "/api/*" "" "$BACKEND_URL_VAL" ""
fi

if [ -n "$VITE_BOTS_VAL" ] && command -v jq >/dev/null 2>&1; then
	# Base64 keeps credentials out of delimiter parsing. Values are decoded and
	# quoted again before entering Caddy syntax.
	printf '%s' "$VITE_BOTS_VAL" | jq -r '
		try fromjson? // .
		| if type == "array" then . else [] end
		| .[]
		| select(type == "object")
		| select((.id // "") | type == "string" and length > 0)
		| select((.baseURL // "") | type == "string" and length > 0)
		| @base64
	' | while IFS= read -r BOT_RECORD; do
		[ -z "$BOT_RECORD" ] && continue
		BOT_JSON=$(printf '%s' "$BOT_RECORD" | base64 -d)
		BOT_ID=$(printf '%s' "$BOT_JSON" | jq -r '.id')
		BOT_URL=$(printf '%s' "$BOT_JSON" | jq -r '.baseURL')
		BOT_TOKEN=$(printf '%s' "$BOT_JSON" | jq -r '.token // ""')

		if ! printf '%s' "$BOT_ID" | grep -Eq '^[A-Za-z0-9_-]+$'; then
			echo "skip invalid WebUI bot id" >&2
			continue
		fi
		if [ "$WEBUI_AUTH_MODE_VAL" != "authelia" ]; then
			BOT_TOKEN=""
		fi

		append_reverse_proxy \
			"/bot/${BOT_ID}/api/*" \
			"/bot/${BOT_ID}" \
			"$BOT_URL" \
			"$BOT_TOKEN"
	done
fi

exec "$@"
