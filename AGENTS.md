# Repository Agent Instructions

## Go build and test baseline

- Before compiling or testing Go code, inspect the available `.vscode/launch.json` and `.vscode/settings.json`. Their active parameters are authoritative for the local workspace.
- Use the Go version declared by `go.mod`; do not rely on another host-default Go version that happens to appear first in `PATH`.
- Apply `-tags=custom_skip_vips` to Go build, run, and test commands.
- Run tests with `BETAGO_CONFIG_PATH=.dev/config.toml` and the active VS Code test flag `-v`.
- Do not enable commented settings. In particular, `.vscode/settings.json` currently comments out `-gcflags=all=-N -l`, so it is not part of the default baseline.
- When working from another worktree, resolve `BETAGO_CONFIG_PATH` to the main workspace's existing `.dev/config.toml`, preferably by absolute path. Never copy, expose, or commit that local configuration file.
- Because `.vscode` is ignored by Git, `README.md` and this file record the repository-visible snapshot. Synchronize both whenever the effective local VS Code baseline changes.
- If `.vscode` is missing, use the documented snapshot above. If local configuration and repository documentation conflict, stop and report the mismatch instead of silently changing parameters or claiming blocked tests passed.

More specific `AGENTS.md` files may add instructions for their subtrees, but they do not remove this repository-wide verification baseline unless they explicitly replace it.
