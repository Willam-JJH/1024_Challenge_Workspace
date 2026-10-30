"""评审打分。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/judging", tags=["judging"])

register_stubs(
    router,
    [
        ("GET", "/projects", "待我评审的作品与我的评分卡"),
        ("PUT", "/projects/{project_id}/scores", "保存评分（1–10）与私密备注"),
    ],
    tag="judging",
    auth="authenticated",
)
