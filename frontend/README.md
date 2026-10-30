# 前端（待重设计）

接口契约已就绪：见 [`../docs/api/openapi.json`](../docs/api/openapi.json)。

计划：

1. React + Vite + TypeScript，从 `openapi.json` 生成类型与请求客户端。
2. 视觉与交互重新设计，不沿用旧站的组件与样式。
3. 开发期通过 Vite 代理把 `/api` 转到 `http://127.0.0.1:8000`。
