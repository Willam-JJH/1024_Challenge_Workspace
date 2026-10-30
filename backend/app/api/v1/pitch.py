"""Pitching Day 直播页。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/pitch", tags=["pitch"])

register_stubs(
    router,
    [
        ("GET", "", "直播页：是否在播、流源、对战与在线人数"),
        ("GET", "/stream", "直播页实时更新（SSE）"),
        ("GET", "/messages", "直播页留言（最新在前）"),
        ("POST", "/messages", "在直播页留言"),
        ("DELETE", "/messages/{message_id}", "撤回我自己的留言"),
        ("GET", "/sources", "全部流源（按区域与顺序）"),
    ],
    tag="pitch",
)
