# ADR 0002：会话、Bearer 与统一主体

- 状态：已采纳
- 日期：2026-10-30

## 背景

原实现有三套身份入口：网站 cookie 会话、个人 API key（Bearer）、桌面端 OAuth 访问令牌，
判定逻辑分散在多个插件里，权限边界难以复核。

## 决策

1. 两种凭据在同一个依赖 `get_principal_optional` 中解析，产出统一 `Principal`。
2. 会话令牌与 API key 在库中只存 SHA-256 哈希；API key 明文只在创建时返回一次。
3. 权限按角色等级（participant → admin）粗粒度映射，细粒度用 `require_scope(...)`，
   后续可平滑替换为权限表。
4. 口令哈希使用标准库 scrypt，不引入额外依赖。

## 影响

- 任意入口的鉴权行为可在一处复核。
- 会话表保留 `user_agent`、`client_ip`，支持「我的设备」与强制登出。
- 在 Cloudflare 之后时，`client_ip` 仅在连接来自受信代理时才采信 `CF-Connecting-IP`。
