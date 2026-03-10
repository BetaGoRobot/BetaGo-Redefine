# ADR 0002: Identity Model

## Status
Accepted

## Decision
- 用户身份主键统一为 `OpenID`
- 机器人租户边界统一为 `AppID + BotOpenID`
- tool-call、config、permission、task、scheduler、private mode 都必须以这一套语义为准

## Consequences
- 旧 `UserId` 仅允许 fallback
- identity 缺失时优先 fail-closed，而不是无租户兜底查询
