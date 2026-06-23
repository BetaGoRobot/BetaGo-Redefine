# BetaGo WebUI 前端

BetaGo 管理后台的前端工程，与后端 `internal/interfaces/webui` 暴露的 REST API 前后端分离部署。

## 功能

- 列出机器人加入的所有群聊（含头像、chat_id、状态、成员数）
- 选择群聊查看统计数据与 token 消耗分布（按模型 / 类型 / 来源 / 状态 / 按天）
- 查看与修改群聊的功能开关
- 查看与修改群聊的配置项（支持数值 / 开关 / 枚举 / 自定义）

## 技术栈

Vite + Vue 3 + TypeScript + Pinia + Vue Router + Element Plus + ECharts + axios

## 本地开发

```bash
cd webui
cp .env.example .env.local   # 按需修改 VITE_API_BASE
npm install
npm run dev
```

默认 dev server 监听 5173，并通过 `vite.config.ts` 把 `/api` 代理到后端（默认 `http://localhost:8090`）。
也可以直接设置 `VITE_API_BASE` 指向后端地址，跨域由后端 `webui_config.cors_allow_origins` 控制。

## 鉴权

后端配置 `webui_config.auth_token` 后，写操作（开关 / 配置修改）需要 Bearer Token。
首次进行写操作时，前端会提示输入 Token，保存在浏览器 localStorage（key: `betago_webui_token`）。

## 构建

```bash
npm run build      # 产物输出到 dist/
npm run preview    # 预览构建产物
```

`dist/` 已在 `.gitignore` 中忽略，部署时由 CI/静态服务器托管。

## 镜像构建与部署

前端镜像由 `script/webui/Dockerfile` 构建：node 构建出静态产物，再用 Caddy 托管 SPA，
并把 `/api` 反代到后端（运行时通过环境变量 `BACKEND_URL` 注入，默认 `http://larkrobot:8090`）。
同一个镜像可指向不同后端，无需在构建期绑定后端地址。

CI 工作流 `.github/workflows/docker-image-webui.yaml` 会在 `webui/**` 或 `script/webui/**`
变更时构建并推送到 `kevinmatt/betago_webui`（`latest` 与带时间戳的 `latest-<ts>` 两个 tag）。

本地构建：

```bash
# 在仓库根目录执行（context 是根目录，Dockerfile 在 script/webui/）
docker build -f script/webui/Dockerfile -t kevinmatt/betago_webui:latest .
```

拉取并运行（示意）：

```bash
docker pull kevinmatt/betago_webui:latest

# 指向后端 WebUI 端口（与后端 [webui_config] addr 对应）
docker run -d --name betago-webui \
  -p 8080:80 \
  -e BACKEND_URL="http://你的后端地址:8090" \
  kevinmatt/betago_webui:latest

# 浏览器访问 http://localhost:8080
```

若前后端在同一 docker 网络，`BACKEND_URL` 可直接用后端容器名，例如 `http://larkrobot:8090`。

