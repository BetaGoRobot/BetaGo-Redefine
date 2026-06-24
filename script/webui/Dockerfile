# BetaGo WebUI 前端镜像（Caddy 版）。
#
# 多阶段：node 构建出静态产物 dist/，再用 caddy 托管 SPA 并把 /api 反代到后端。
# 后端地址、bot 预设等在容器**运行时**通过环境变量注入：
#   - BACKEND_URL：默认 http://larkrobot:8090，由 Caddyfile 直接读取做反代上游。
#   - VITE_BOTS：JSON 数组字符串，与构建期 VITE_BOTS 同格式；entrypoint 会在
#                启动时渲染到 /config.js，浏览器加载后通过 window.__BETAGO_CONFIG__ 读取。
#   - VITE_API_BASE：可选；为空时前端走同源 /api。
#
# 设计原则：同一个镜像可以指向不同后端、不同 bot 列表，无需重新 build。

FROM node:22-alpine AS builder

WORKDIR /app/webui

# 先拷贝依赖描述并安装，最大化利用构建缓存。
# 依赖源固定为公网 registry（见 webui/.npmrc），不依赖任何内网源。
COPY webui/package.json webui/package-lock.json webui/.npmrc ./
RUN npm ci --no-audit --no-fund

# 再拷贝源码并构建。前端走相对路径 /api，跨域/转发交给 caddy。
# 注意：构建期不再需要 VITE_BOTS；该值改为运行时注入。
COPY webui/ ./
RUN npm run build

FROM caddy:2-alpine AS runner

# entrypoint 用 jq 把任意字符串安全编码成 JS 字面量。
RUN apk add --no-cache jq

# Caddyfile 里用 {$BACKEND_URL} 读取运行时环境变量。
COPY script/webui/Caddyfile /etc/caddy/Caddyfile
COPY script/webui/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh \
	&& touch /etc/caddy/bots.caddy \
	&& chmod 644 /etc/caddy/bots.caddy
COPY --from=builder /app/webui/dist /usr/share/caddy

ENV BACKEND_URL="http://larkrobot:8090"

EXPOSE 80

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
