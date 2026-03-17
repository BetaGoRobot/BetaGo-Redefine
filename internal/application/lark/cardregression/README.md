# Card Regression

`internal/application/lark/cardregression` 提供统一的卡片回归协议。

目标：

- 用 `CardSceneProtocol` 强制卡片场景实现 `BuildTestCard(...)`
- 用 `Registry` 统一注册 canonical scene key
- 用 `Runner` 按 `scene/case/suite` 执行 dry-run 或直发回归

当前 CLI 入口：

```bash
go run ./cmd/lark-card-debug --list-scenes
go run ./cmd/lark-card-debug --scene config.list --case smoke-default --dry-run
go run ./cmd/lark-card-debug --suite smoke --dry-run --report-json /tmp/card-regression.json
```

约定：

- scene key: `<domain>.<action>`
- case name: `smoke-default` / `live-default` / `sample-default`
- `--spec` 只做 legacy alias，registry 只认 canonical scene key
