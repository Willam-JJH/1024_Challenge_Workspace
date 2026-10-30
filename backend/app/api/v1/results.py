"""结果与入围名单。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/results", tags=["results"])

register_stubs(
    router,
    [
        ("GET", "", "已公布的结果：奖项与最终榜"),
        ("GET", "/shortlist", "已公布的入围名单（按组织者顺序）"),
        ("POST", "/shortlist/{project_id}/attendance", "回答入围作品的出席问题"),
    ],
    tag="results",
)
