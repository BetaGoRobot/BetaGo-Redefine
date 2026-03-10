# ADR 0003: Config Access

## Status
Accepted

## Decision
- 运行时业务逻辑禁止直接读取 TOML struct
- 动态配置统一通过 config manager / accessor 读取
- 配置优先级固定为 `chat:user > user > chat > global > toml/default`
- bot namespace 是配置键的一部分

## Consequences
- 新功能必须先声明 config key，再接入 accessor
- identity 缺失时不允许跨 bot 读取 dynamic config
