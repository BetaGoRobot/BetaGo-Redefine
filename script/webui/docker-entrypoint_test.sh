#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT INT TERM

PUBLIC_DIR="$TEST_ROOT/public"
PRIVATE_DIR="$TEST_ROOT/private"
CONFIG_FILE="$PUBLIC_DIR/config.js"
BOTS_FILE="$PRIVATE_DIR/bots.caddy"
AUTH_GATE_FILE="$PRIVATE_DIR/auth-gate.caddy"

mkdir -p "$PUBLIC_DIR" "$PRIVATE_DIR"

WEBUI_AUTH_MODE=authelia \
BACKEND_URL='http://default-private-upstream-sentinel:8090' \
BACKEND_AUTH_TOKEN='default-secret-sentinel' \
VITE_API_BASE='' \
VITE_BOTS='[{"id":"bot-one","name":"Bot One","baseURL":"http://bot-private-upstream-sentinel:8090","token":"bot-secret-sentinel","api_key":"api-key-sentinel","password":"password-sentinel","secret":"secret-sentinel","unknown":"unknown-sentinel","remark":"Primary"}]' \
CONFIG_DIR="$PUBLIC_DIR" \
BOTS_CADDY_FILE="$BOTS_FILE" \
AUTH_GATE_CADDY_FILE="$AUTH_GATE_FILE" \
	sh "$REPO_ROOT/script/webui/docker-entrypoint.sh" true

assert_contains() {
	file=$1
	value=$2
	message=$3
	if ! grep -Fq "$value" "$file"; then
		echo "FAIL: $message" >&2
		exit 1
	fi
}

assert_not_contains() {
	file=$1
	value=$2
	message=$3
	if grep -Fq "$value" "$file"; then
		echo "FAIL: $message" >&2
		exit 1
	fi
}

assert_contains "$CONFIG_FILE" 'authMode: "authelia"' \
	"public runtime config must declare authelia mode"
assert_contains "$CONFIG_FILE" '"id":"bot-one"' \
	"public runtime config must retain the bot id"
assert_contains "$CONFIG_FILE" '"name":"Bot One"' \
	"public runtime config must retain the bot name"
assert_contains "$CONFIG_FILE" '"remark":"Primary"' \
	"public runtime config must retain public metadata"

for private_value in \
	'default-private-upstream-sentinel' \
	'bot-private-upstream-sentinel' \
	'default-secret-sentinel' \
	'bot-secret-sentinel' \
	'api-key-sentinel' \
	'password-sentinel' \
	'secret-sentinel' \
	'unknown-sentinel'
do
	assert_not_contains "$CONFIG_FILE" "$private_value" \
		"public runtime config contains a private value"
done

assert_contains "$BOTS_FILE" 'bot-private-upstream-sentinel' \
	"private Caddy config must retain the bot upstream"
assert_contains "$BOTS_FILE" 'Bearer bot-secret-sentinel' \
	"private Caddy config must inject the per-bot bearer credential"
assert_contains "$BOTS_FILE" 'default-private-upstream-sentinel' \
	"private Caddy config must retain the default upstream"
assert_contains "$BOTS_FILE" 'Bearer default-secret-sentinel' \
	"private Caddy config must inject the default bearer credential"

assert_contains "$AUTH_GATE_FILE" 'Remote-User' \
	"secure mode must generate an authenticated-user gate"
assert_contains "$AUTH_GATE_FILE" '/auth/session' \
	"secure mode must protect the session probe"
assert_contains "$AUTH_GATE_FILE" '/auth/login' \
	"secure mode must protect the login bridge"

LEGACY_PUBLIC_DIR="$TEST_ROOT/legacy-public"
LEGACY_PRIVATE_DIR="$TEST_ROOT/legacy-private"
mkdir -p "$LEGACY_PUBLIC_DIR" "$LEGACY_PRIVATE_DIR"

WEBUI_AUTH_MODE=legacy \
BACKEND_URL='http://legacy-default:8090' \
VITE_BOTS='[{"id":"legacy","name":"Legacy","baseURL":"http://legacy-bot:8090","token":"legacy-browser-token"}]' \
CONFIG_DIR="$LEGACY_PUBLIC_DIR" \
BOTS_CADDY_FILE="$LEGACY_PRIVATE_DIR/bots.caddy" \
AUTH_GATE_CADDY_FILE="$LEGACY_PRIVATE_DIR/auth-gate.caddy" \
	sh "$REPO_ROOT/script/webui/docker-entrypoint.sh" true

assert_contains "$LEGACY_PUBLIC_DIR/config.js" 'legacy-browser-token' \
	"legacy mode must preserve browser-owned token compatibility"
assert_not_contains "$LEGACY_PRIVATE_DIR/bots.caddy" 'Bearer legacy-browser-token' \
	"legacy mode must not overwrite the browser Authorization header"
if [ -s "$LEGACY_PRIVATE_DIR/auth-gate.caddy" ]; then
	echo "FAIL: legacy mode must not install the Authelia Caddy gate" >&2
	exit 1
fi

echo "docker-entrypoint secure projection tests passed"
