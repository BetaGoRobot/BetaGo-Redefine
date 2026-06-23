# BetaGo WebUI 前端镜像（Caddy 版）。
#
# 多阶段：node 构建出静态产物 dist/，再用 caddy 托管 SPA 并把 /api 反代到后端。
# 后端地址在容器运行时通过环境变量 BACKEND_URL 注入（默认 http://larkrobot:8090），
# 因此同一个镜像可以指向不同后端，无需在构建期绑定 VITE_API_BASE。

FROM node:22-alpine AS builder

WORKDIR /app/webui

# 先拷贝依赖描述并安装，最大化利用构建缓存。
# 升级 npm 规避 node:22-alpine 自带 npm 10.x 在容器里 `npm ci` 偶发
# "Exit handler never called!" 的已知崩溃；同时关掉 audit/fund 提速减噪。
COPY webui/package.json webui/package-lock.json ./
RUN npm install -g npm@11 && \
    npm ci --no-audit --no-fund

# 再拷贝源码并构建。前端走相对路径 /api，跨域/转发交给 caddy。
COPY webui/ ./
RUN npm run build

FROM caddy:2-alpine AS runner

# Caddyfile 里用 {$BACKEND_URL} 读取运行时环境变量。
COPY script/webui/Caddyfile /etc/caddy/Caddyfile
COPY --from=builder /app/webui/dist /usr/share/caddy

ENV BACKEND_URL="http://larkrobot:8090"

EXPOSE 80

CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
