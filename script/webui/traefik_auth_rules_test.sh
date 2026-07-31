#!/bin/sh
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
RENDERED=$(mktemp)

WEBUI_HOST='webui.test.example' \
WEBUI_AUTH_MODE='authelia' \
AUTHELIA_MIDDLEWARE='authelia@docker' \
BOT_WEBUI_AUTH_TOKEN='compose-secret-sentinel' \
	docker compose -f "$REPO_ROOT/deploy/docker-compose.yaml" config >"$RENDERED"

assert_contains() {
	value=$1
	message=$2
	if ! grep -Fq "$value" "$RENDERED"; then
		echo "FAIL: $message" >&2
		exit 1
	fi
}

assert_not_contains() {
	value=$1
	message=$2
	if grep -Fq "$value" "$RENDERED"; then
		echo "FAIL: $message" >&2
		exit 1
	fi
}

assert_contains 'WEBUI_AUTH_MODE: authelia' \
	"WebUI secure mode must reach the container"
assert_contains 'BACKEND_AUTH_TOKEN: compose-secret-sentinel' \
	"default Bot bearer must reach the private WebUI gateway"
assert_contains 'traefik.http.routers.betago-webui.priority: "10"' \
	"public router must have an explicit low priority"
assert_contains 'traefik.http.routers.betago-webui-protected.priority: "100"' \
	"protected router must have an explicit high priority"
assert_contains 'Method(`PUT`)' "protected router must cover PUT"
assert_contains 'Method(`POST`)' "protected router must cover POST"
assert_contains 'Method(`PATCH`)' "protected router must cover PATCH"
assert_contains 'Method(`DELETE`)' "protected router must cover DELETE"
assert_contains '/auth/session' "protected router must cover the session probe"
assert_contains '/auth/login' "protected router must cover the login bridge"
assert_contains 'evaluations' "protected router must cover evaluations"
assert_contains 'members' "protected router must cover member identities"
assert_contains 'configs' "protected router must cover configuration values"
assert_contains 'features' "protected router must cover feature state"
assert_contains 'agentic-rollout' "protected router must cover rollout state"
assert_contains 'top_senders' "protected router must cover sender identities"
assert_contains 'top_mentions' "protected router must cover mention identities"
assert_contains 'betago-webui-strip-auth-headers@docker,authelia@docker' \
	"protected router must strip spoofed identity before ForwardAuth"
assert_contains 'traefik.http.middlewares.betago-webui-strip-auth-headers.headers.customrequestheaders.Remote-User: ""' \
	"public requests must not be able to spoof Remote-User"
assert_not_contains 'target: 8090' \
	"production compose must not publish the Bot backend port"

echo "Traefik selective auth route tests passed"
