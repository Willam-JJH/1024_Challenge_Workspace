"""投票（公开榜单）。个人钱包在 ``/me/wallet``，管理端在 ``/admin/voting``。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/votes", tags=["votes"])

register_stubs(
    router,
    [
        ("GET", "", "实时投票榜：各公司总数、名次与最近变化"),
        ("GET", "/big-screen", "大屏仪式视图：前 N 名与名次变化"),
        ("GET", "/stream", "投票榜 SSE 推送"),
    ],
    tag="votes",
)
