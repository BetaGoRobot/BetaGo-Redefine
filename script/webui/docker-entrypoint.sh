#!/bin/sh
# WebUI 容器启动入口：把运行时环境变量（VITE_BOTS / VITE_API_BASE）渲染成
# /config.js，写入 Caddy 静态根目录，再交给 caddy 接管。
#
# 设计目标：把 Vue 这种构建期框架对环境变量的耦合，搬到部署期。
# 同一个镜像可以指向不同后端 / 不同 bot 列表，无需重新 build。
set -eu

CONFIG_DIR="${CONFIG_DIR:-/usr/share/caddy}"
CONFIG_FILE="${CONFIG_DIR}/config.js"

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

exec "$@"
