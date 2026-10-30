# ADR 0001：重写后端，接口契约先行

- 状态：已采纳
- 日期：2026-10-30

## 背景

平台功能（139 个提交、约 1.7 万行）在原 Unscripted 仓库中完成，但该仓库没有 LICENSE，
代码默认保留所有权利；原实现是 Next.js + Fastify + Drizzle 的 TypeScript monorepo，
与本人日常使用的 Python 工具链（FastAPI、SQLAlchemy、uv）不一致。

## 决策

1. **不复制原实现**：领域模型按语义重写，接口按新规范重新设计。
2. **接口契约先行**：先把全部端点以可运行的桩形式注册，OpenAPI 完整可见，再逐模块实现。
3. **技术栈**：FastAPI + SQLAlchemy 2.0 + Alembic + PostgreSQL；前端后续用 React 重设计。
4. **暂不迁移桌面端**（原 `apps/intern` 是 opencode 的 MIT fork，体积 84 MB）。

## 影响

- 接口数量从 267 个旧端点重组为 292 个新端点，路径与鉴权语义全部重新设计。
- 前端可以从零按 OpenAPI 生成类型，不受旧实现的历史包袱影响。
- 未实现端点返回 `501 not_implemented`，不会误报成功。
