# BetaGo 部署

## docker compose 一键部署

`docker-compose.yaml` 编排了后端机器人、WebUI 前端与核心基础设施：

| 服务 | 镜像 | 说明 |
|------|------|------|
| `postgres` | postgres:16-alpine | 首次启动自动执行 `script/migrations/` 下的迁移 SQL |
| `redis` | redis:7-alpine | 频控 / chunk 会话缓存 |
| `larkrobot` | kevinmatt/larkbot_v2 | 后端机器人 + WebUI API（端口 8090） |
| `webui` | kevinmatt/betago_webui | Caddy 托管前端，`/api` 反代到 `larkrobot:8090`，对外由 Traefik 反代 |

> OpenSearch / Minio / 网易云 API 等可选依赖未内置，按需在 `config.toml` 中指向外部实例。

### 步骤

```bash
cd deploy
cp config.example.toml config.toml   # 填入 lark / ark 等真实凭据（host 用服务名 postgres/redis）
cp .env.example .env                 # 按需改域名 / 密码 / 镜像 tag
docker compose up -d
```

- WebUI：`https://${WEBUI_HOST}`（经前置 Traefik，websecure/443 + TLS）
- 后端管理 API（直连调试）：`http://localhost:8090/api/...`（`WEBUI_BACKEND_PORT`）

### Traefik 反代

`webui` 服务已带 Traefik docker-provider 的 labels（方案 1：整域名交给本容器，Caddy 内部再分静态与 `/api`）：

- 路由规则 `Host(${WEBUI_HOST})`，entrypoint `websecure`，TLS certresolver `${TRAEFIK_CERTRESOLVER}`
- 转发到容器 80 端口
- 默认不再把 webui 端口暴露到宿主；如需本地直连，取消 compose 里 `webui.ports` 注释

> 前提：Traefik 与本套服务在同一 docker 网络且其 docker provider 已开启。
> 若 Traefik 在独立外部网络，需要给服务补 `networks` 并把该网络标为 `external`。

`config.toml` 与 `.env` 含真实凭据，已在 `.gitignore` 忽略。

### 常用命令

```bash
docker compose pull            # 拉取最新镜像
docker compose up -d           # 启动 / 更新
docker compose logs -f webui   # 看前端容器日志
docker compose logs -f larkrobot
docker compose down            # 停止（保留数据卷）
docker compose down -v         # 停止并清除 pgdata/redisdata
```

### 鉴权

后端 `config.toml` 配了 `webui_config.auth_token` 后，前端写操作（改开关 / 配置）需要在页面右上角「设置 Token」填入同一值；只读浏览不需要。

## Grafana 大盘

`grafana/` 下提供两块大盘 JSON，导入 Grafana 即可：

- `betago-bystage-duration.json`：各阶段耗时
- `betago-llm-token-usage.json`：LLM token 用量

## Conversation Runtime 与并轨评测初始化

部署不需要执行 SQL、生成 GORM 文件或调用 OpenSearch API。应用启动时会：

- 在 PostgreSQL advisory lock 下幂等执行内嵌迁移；
- 根据 `lark_config.app_id + bot_open_id` 派生稳定的 `tenant_id`；
- 为会话事件和并轨评测自动创建当前 Bot 独占的 physical index 与 write alias；
- 在 `evaluation_mode` 允许的群首次收到消息时，自动创建滚动 cohort。

`conversation_event_index` 与 `evaluation_index` 配置的是 base name。多个 Bot
可以填写同一个 base name，运行时会自动追加不可冲突的租户后缀。应用重启时
复用已有 schema、index 和 alias，不重复创建。若自动初始化失败，相关模块会
明确启动失败，避免带着半初始化状态对外服务。

并轨评测仍以 PostgreSQL 为事实源；OpenSearch 保存可重建的检索快照。文档
包含 `tenant_id/app_id/bot_open_id`，文档 ID 也带租户前缀。
