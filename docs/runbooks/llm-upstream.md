# 把 new-api 接成网关上游

本机已经在 Docker 里跑着 **new-api v1.0.0-rc.38**（容器 `new-api`，端口 `3000`，配 postgres + redis）。

## 1. 在 new-api 里建令牌

浏览器打开 `http://localhost:3000` → 登录管理账号 → **令牌** → 新建，模型范围选你要开放的模型，
复制 `sk-...`。

## 2. 在我们这边登记上游

```bash
curl -X POST http://127.0.0.1:8000/api/v1/admin/llm/upstreams \
  -H "Authorization: Bearer sk_<你的管理员 API key>" \
  -H "Content-Type: application/json" \
  -d '{"name":"new-api","base_url":"http://localhost:3000","api_key":"sk-<new-api 令牌>"}'
```

`base_url` 不带 `/v1`：网关会自己拼 `/v1/chat/completions`。

## 3. 登记模型与路由

```bash
curl -X POST .../api/v1/admin/llm/models \
  -d '{"name":"qwen3-coder","input_price_micro":100,"output_price_micro":300,"context_window":44000}'

curl -X POST .../api/v1/admin/llm/models/<model_id>/routes \
  -d '{"upstream_id":"<upstream_id>","upstream_model":"qwen3-coder"}'
```

价格按**每百万 token 的微美元**填（`100` = 0.0001 美元/百万 token）。预算与熔断都在我们这边，
new-api 只管转发与渠道轮换。

## 4. 计费口径

- 我们的 `/v1/chat/completions` 从 `usage` 字段读 token 数，按上面的单价写 `llm_usage`。
- 赛事预算 `APP` 侧存在 `events.llm_budget_micro`；余额 = 预算 − 已用。
- 熔断：全局（`events.llm_kill_switch`）、单模型（`llm_models.enabled`）、单人（`users.llm_blocked`）。
