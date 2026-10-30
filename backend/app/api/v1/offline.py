"""线下活动场次与报名。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/offline-sessions", tags=["offline"])

register_stubs(
    router,
    [
        ("GET", "", "当前赛事的线下场次（带我的回答）"),
        ("GET", "/{session_id}", "单个线下场次"),
        ("GET", "/{session_id}/calendar.ics", "导出为 iCalendar"),
        ("PUT", "/{session_id}/rsvp", "回答参加／待定／不参加（满员进入候补）"),
        ("DELETE", "/{session_id}/rsvp", "取消报名"),
    ],
    tag="offline",
)
