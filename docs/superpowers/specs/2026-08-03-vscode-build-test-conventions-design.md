# VS Code 编译与测试约定设计

## 目标

让开发者和代码代理使用与本地 VS Code 工作区一致的 Go 编译、测试参数，避免因宿主机默认工具链、缺失的 GLib/VIPS 开发包或错误的配置路径产生误判。

## 事实来源

本机 `.vscode/launch.json` 和 `.vscode/settings.json` 是编译、调试与测试参数的事实来源。由于 `.vscode` 被 `.gitignore` 忽略，仓库中的 `README.md` 和根目录 `AGENTS.md` 必须记录当前有效参数；本机 `.vscode` 发生变化时，应同步更新这两份文档。

Go 语言版本仍由 `go.mod` 声明，不使用宿主机偶然排在 `PATH` 首位的其他版本。

## 当前参数映射

- 所有会编译 Go 包的开发命令，包括 `go build`、`go run` 和 `go test`，使用 `-tags=custom_skip_vips`。
- 测试设置 `BETAGO_CONFIG_PATH=.dev/config.toml`。
- 测试使用 `.vscode/settings.json` 中启用的 `-v` 参数。
- `.vscode/settings.json` 中注释掉的 `-gcflags=all=-N -l` 不属于当前默认参数，代理不得自行启用。
- 从其他 worktree 执行测试时，`BETAGO_CONFIG_PATH` 应解析为主工作区实际存在的 `.dev/config.toml`，但不得复制或提交该本地配置文件。

## 文档落点

`README.md` 在开发调试或 CI/构建说明附近增加面向开发者的“编译与测试基线”，给出参数和等价 CLI 示例。

根目录 `AGENTS.md` 增加面向代码代理的强制规则：运行编译或测试前先读取可用的 `.vscode` 配置，按上述参数执行；如果配置缺失或矛盾，停止并报告，不能擅自换参数后宣称验证通过。

## 验证

- 对照本机 `.vscode/launch.json` 和 `.vscode/settings.json` 检查 README 与 `AGENTS.md` 的参数一致性。
- 运行 `git diff --check`。
- 本次只修改文档约定，不改变产品代码；已有产品测试结果继续有效。
