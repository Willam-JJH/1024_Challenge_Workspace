"""管理台：LLM 网关的预算、模型、上游与熔断。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/admin/llm", tags=["admin"])

register_stubs(
    router,
    [
        ("GET", "/overview", "花费、剩余额度、燃烧速率、预测与分解"),
        ("GET", "/budget", "赛事预算"),
        ("PUT", "/budget", "设置赛事预算（微美元）"),
        ("GET", "/models", "模型列表（含价格与路由）"),
        ("POST", "/models", "新增公开模型"),
        ("PATCH", "/models/{model_id}", "编辑模型（价格、限额、能力、启用）"),
        ("DELETE", "/models/{model_id}", "删除模型"),
        ("POST", "/models/{model_id}/routes", "把模型映射到上游模型（按优先级回退）"),
        ("PATCH", "/routes/{route_id}", "编辑路由"),
        ("DELETE", "/routes/{route_id}", "删除路由"),
        ("GET", "/upstreams", "上游列表（密钥掩码）"),
        ("POST", "/upstreams", "新增 OpenAI 兼容上游（密钥只写）"),
        ("PATCH", "/upstreams/{upstream_id}", "编辑上游；传 api_key 即替换密钥"),
        ("DELETE", "/upstreams/{upstream_id}", "删除上游及其路由"),
        ("GET", "/kill-switches", "熔断开关状态"),
        ("PUT", "/kill-switches/global", "总开关"),
        ("PUT", "/kill-switches/models/{model_id}", "单模型开关"),
        ("PUT", "/kill-switches/users/{user_id}", "单人开关"),
        ("GET", "/usage/teams", "按公司统计 token 消耗与燃烧速率"),
        ("GET", "/usage/users", "按人统计消耗（最贵在前）"),
    ],
    tag="admin",
    auth="staff",
)
