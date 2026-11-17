# 接口规范

## 路径

- 业务接口统一 `/api/v1` 前缀；LLM 网关的 OpenAI 兼容面单独在 `/v1`。
- 资源用复数名词、kebab-case：`/companies`、`/offline-sessions`、`/api-keys`。
- 嵌套最多两层，且只在「从属关系」时使用：`/companies/{id}/invitations`。
- 动作型操作用子资源表示，动词只出现在最后一段：`/projects/{id}/edit-sessions`、
  `/announcements/{id}`（而不是 `/announcements/:id/delete`）。
- ID 一律字面量字符串；对用户可见的地址另给 `slug`。

## 鉴权

| 调用方 | 凭据 | 说明 |
|---|---|---|
| 浏览器 | `sid` cookie（HttpOnly, SameSite=Lax） | 登录成功时写入，退出时清除 |
| CLI / 代理 | `Authorization: Bearer sk_...` | API key，数据库只存 SHA-256 哈希 |
| 桌面端 | `Authorization: Bearer <oauth access token>` | 授权码 + PKCE，走 `/oauth/*` |

角色等级：`participant < member < captain < staff < admin`。依赖项：

- `require_auth` —— 必须登录
- `require_staff` —— 组织者及以上
- `require_scope("project:write")` —— 细粒度权限（按等级映射）

## 错误

所有错误统一信封，HTTP 状态码保持语义：

```json
{
  "error": {
    "code": "not_found",
    "message": "作品不存在",
    "details": {},
    "request_id": "0f2c..."
  }
}
```

`request_id` 同时写在响应头 `X-Request-ID`，由网关或调用方透传。

## 分页

```
GET /api/v1/companies?limit=20&cursor=...
```

```json
{ "items": [...], "next_cursor": "eyJpZCI6..." }
```

`limit` 上限 100，默认 20；`next_cursor` 为空表示没有下一页。

## 并发

写操作使用 `If-Match: <revision>`；不匹配返回 `409 conflict`。
编辑锁是显式资源（`/projects/{id}/edit-sessions`），带心跳与 TTL，不再用隐式状态。

## 时间与 IDs

- 时间一律 ISO 8601 UTC（`2026-10-30T08:00:00Z`）。
- 主键为 32 位十六进制 UUID4，不暴露自增序号。

## 赛季

平台按届运行，一届是一个 `event`。完整的生命周期见
[`../design/season-lifecycle.md`](../design/season-lifecycle.md)，接口层面只需要记住三条：

- **当期赛事由 `APP_CURRENT_EVENT_SLUG` 决定**，不是由时间推算。画廊、实时动态、报名、
  投票只作用于当期赛事。
- **报名按届记录**（`event_enrollments`）：`POST /events/{id}/enrollments` 报参赛者或观众，
  受 `registration_open` 约束，比赛结束后不再接受。
- **往届作品走 `/exhibition/archive`**：赛事结束满保留期（默认 30 天）后写入
  `events.archived_at`，那一届的作品从进行中的页面移出，只出现在归档接口里。

## 版本演进

- 破坏性变更才升 `/api/v2`；新增字段、可选参数不算破坏性变更。
- 每个版本在 OpenAPI 中完整可见；`scripts/export_openapi.py` 生成 `docs/api/openapi.json` 并做回归比对。
- 只在 Go 侧实现的接口写在 [`openapi-additions.json`](openapi-additions.json)，导出脚本会把
  这份覆盖层并进规范。Python 实现已进入退场流程，新能力不再两边都写。
