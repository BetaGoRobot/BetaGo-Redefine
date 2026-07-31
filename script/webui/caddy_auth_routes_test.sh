#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
TEST_ROOT=$(mktemp -d)
CONTAINER_ID=""

cleanup() {
	if [ -n "$CONTAINER_ID" ]; then
		docker stop "$CONTAINER_ID" >/dev/null 2>&1 || true
	fi
	rm -r -- "$TEST_ROOT"
}
trap cleanup EXIT INT TERM

PUBLIC_DIR="$TEST_ROOT/public"
PRIVATE_DIR="$TEST_ROOT/private"
mkdir -p "$PUBLIC_DIR" "$PRIVATE_DIR"
printf '<!doctype html><title>BetaGo route test</title>' >"$PUBLIC_DIR/index.html"

WEBUI_AUTH_MODE=authelia \
BACKEND_URL='http://127.0.0.1:65534' \
BACKEND_AUTH_TOKEN='default-server-secret' \
VITE_BOTS='[{"id":"main","name":"Main","baseURL":"http://127.0.0.1:65534","token":"bot-server-secret"}]' \
CONFIG_DIR="$PUBLIC_DIR" \
BOTS_CADDY_FILE="$PRIVATE_DIR/bots.caddy" \
AUTH_GATE_CADDY_FILE="$PRIVATE_DIR/auth-gate.caddy" \
	sh "$REPO_ROOT/script/webui/docker-entrypoint.sh" true

CONTAINER_ID=$(docker run --rm -d -p 127.0.0.1::80 \
	-v "$REPO_ROOT/script/webui/Caddyfile:/etc/caddy/Caddyfile:ro" \
	-v "$PRIVATE_DIR/bots.caddy:/etc/caddy/bots.caddy:ro" \
	-v "$PRIVATE_DIR/auth-gate.caddy:/etc/caddy/auth-gate.caddy:ro" \
	-v "$PUBLIC_DIR:/usr/share/caddy:ro" \
	caddy:2-alpine \
	caddy run --config /etc/caddy/Caddyfile --adapter caddyfile)

PORT=$(docker inspect "$CONTAINER_ID" \
	--format '{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostPort}}')

request_status() {
	curl --noproxy '*' -sS -o "$TEST_ROOT/response" -w '%{http_code}' "$@"
}

attempt=0
while :; do
	attempt=$((attempt + 1))
	if [ "$(request_status "http://127.0.0.1:$PORT/config.js" 2>/dev/null)" = "200" ]; then
		break
	fi
	if [ "$attempt" -ge 10 ]; then
		echo "FAIL: Caddy did not serve /config.js" >&2
		docker logs "$CONTAINER_ID" >&2
		exit 1
	fi
	sleep 1
done

if grep -Eq 'default-server-secret|bot-server-secret|127\.0\.0\.1:65534' "$TEST_ROOT/response"; then
	echo "FAIL: /config.js exposed private upstream configuration" >&2
	exit 1
fi

if [ "$(request_status "http://127.0.0.1:$PORT/auth/session")" != "401" ]; then
	echo "FAIL: anonymous session probe must be rejected" >&2
	exit 1
fi
if [ "$(request_status -X POST "http://127.0.0.1:$PORT/api/agentic-rollouts/batch")" != "401" ]; then
	echo "FAIL: anonymous write must be rejected before proxying" >&2
	exit 1
fi
if [ "$(request_status "http://127.0.0.1:$PORT/bot/main/api/chats/chat-1/members")" != "401" ]; then
	echo "FAIL: anonymous sensitive read must be rejected before proxying" >&2
	exit 1
fi
if [ "$(request_status -H 'Remote-User: route-test' "http://127.0.0.1:$PORT/auth/session")" != "200" ]; then
	echo "FAIL: authenticated session probe must be accepted" >&2
	exit 1
fi
if ! grep -Fq '{"authenticated":true}' "$TEST_ROOT/response"; then
	echo "FAIL: authenticated session response is invalid" >&2
	exit 1
fi
if [ "$(request_status -H 'Remote-User: route-test' "http://127.0.0.1:$PORT/auth/login")" != "200" ]; then
	echo "FAIL: authenticated login bridge must be accepted" >&2
	exit 1
fi
if ! grep -Fq 'betago:auth-complete' "$TEST_ROOT/response"; then
	echo "FAIL: login bridge must notify the opener" >&2
	exit 1
fi

echo "Caddy selective auth route tests passed"
