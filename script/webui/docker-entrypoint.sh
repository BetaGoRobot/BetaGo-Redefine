#!/bin/sh
# WebUI 容器启动入口：把运行时环境变量（VITE_BOTS / VITE_API_BASE）渲染成
# /config.js，写入 Caddy 静态根目录；同时把每个 bot 的内网 baseURL 渲染成
# /etc/caddy/bots.caddy，被主 Caddyfile 通过 `import bots.caddy` 加载。
#
# 这样：
#   - 浏览器永远只跟 webui 容器同域通信，无需公网暴露任何 bot；
#   - VITE_BOTS 里的 baseURL 只在容器启动时被 Caddy 当上游使用，运维填内网地址即可；
#   - VITE_BOTS / VITE_API_BASE 同步刷新到前端 window.__BETAGO_CONFIG__ 与 Caddy 上游。
set -eu

CONFIG_DIR="${CONFIG_DIR:-/usr/share/caddy}"
CONFIG_FILE="${CONFIG_DIR}/config.js"
BOTS_CADDY_FILE="${BOTS_CADDY_FILE:-/etc/caddy/bots.caddy}"

# 用 jq 把任意环境字符串安全编码成 JS 字面量，避免引号/换行/注入问题。
# jq -Rn .  ：从 stdin 读裸字符串并输出合法 JSON 字符串字面量。
encode_js_string() {
	# 没装 jq 时降级：仅做最小转义，提示用户在镜像中保留 jq。
	if command -v jq >/dev/null 2>&1; then
		printf '%s' "$1" | jq -Rs .
	else
		# 最小转义：反斜杠 -> \\，双引号 -> \"，再用双引号包裹。
		printf '"%s"' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e ':a;N;$!ba;s/\n/\\n/g')"
	fi
}

VITE_BOTS_VAL="${VITE_BOTS:-}"
VITE_API_BASE_VAL="${VITE_API_BASE:-}"

BOTS_JS=$(encode_js_string "$VITE_BOTS_VAL")
API_BASE_JS=$(encode_js_string "$VITE_API_BASE_VAL")

cat >"$CONFIG_FILE" <<EOF
// 由容器 entrypoint 在启动时根据环境变量渲染，请勿手工修改。
// 字段定义见 webui/src/env.d.ts:BetaGoRuntimeConfig。
window.__BETAGO_CONFIG__ = {
  bots: ${BOTS_JS},
  apiBase: ${API_BASE_JS},
};
EOF

# 渲染 Caddy 多 bot 反代片段：
# 对 VITE_BOTS 中每个有 baseURL 的 bot，输出一段
#     handle /bot/<id>/api/* { uri strip_prefix /bot/<id>; reverse_proxy <baseURL> }
# 没有 jq 时，跳过生成（多 bot 反代不可用，退化到只用 /api 主后端）。
mkdir -p "$(dirname "$BOTS_CADDY_FILE")"
: >"$BOTS_CADDY_FILE"

if [ -n "$VITE_BOTS_VAL" ] && command -v jq >/dev/null 2>&1; then
	# jq 解析数组：跳过非对象、缺 id 或 baseURL 为空的项。
	# 输出形如：<id>\t<baseURL> 每行一条。
	echo "$VITE_BOTS_VAL" | jq -r '
		try fromjson? // .
		| if type == "array" then . else [] end
		| .[]
		| select(type == "object")
		| select((.id // "") | tostring | length > 0)
		| select((.baseURL // "") | tostring | length > 0)
		| "\(.id)\t\(.baseURL)"
	' | while IFS=$(printf '\t') read -r BOT_ID BOT_URL; do
		[ -z "$BOT_ID" ] && continue
		[ -z "$BOT_URL" ] && continue
		# Caddy 把 path 里的 {} 当 placeholder，bot id 要避免特殊字符。
		# 这里假定 id 是 [A-Za-z0-9_-]+；若需 unicode id，调用方应自行 url-encode。
		cat >>"$BOTS_CADDY_FILE" <<CADDY
handle /bot/${BOT_ID}/api/* {
	uri strip_prefix /bot/${BOT_ID}
	reverse_proxy ${BOT_URL}
}
CADDY
	done
fi

exec "$@"
