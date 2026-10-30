"""投票榜单：实时榜、大屏视图与 SSE 推送。

个人投票记录在 ``/me/votes``；管理端在 ``/admin/voting``。
"""

import asyncio
import json

from fastapi import APIRouter, Depends, Query
from fastapi.responses import StreamingResponse
from sqlalchemy.orm import Session as DbSession

from app.core.db import SessionLocal, get_db
from app.schemas.vote import BoardOut, BoardRow
from app.services import companies as company_service
from app.services import votes as vote_service

router = APIRouter(prefix="/votes", tags=["votes"])


def _board_out(db: DbSession, event) -> BoardOut:  # noqa: ANN001
    payload = vote_service.board_payload(db, event)
    if not event.votes_visible:
        for row in payload["items"]:
            row["votes"] = 0
            row["rank"] = 0
        payload["total_votes"] = 0
    return BoardOut(
        items=[BoardRow(**row) for row in payload["items"]],
        total_votes=payload["total_votes"],
        updated_at=payload["updated_at"],
    )


@router.get("", response_model=BoardOut, summary="实时投票榜")
def get_board(db: DbSession = Depends(get_db)) -> BoardOut:
    return _board_out(db, company_service.current_event(db))


@router.get("/big-screen", response_model=BoardOut, summary="大屏仪式视图：前 N 名")
def get_big_screen(
    n: int = Query(default=10, ge=1, le=50), db: DbSession = Depends(get_db)
) -> BoardOut:
    board = _board_out(db, company_service.current_event(db))
    board.items = board.items[:n]
    return board


@router.get("/stream", summary="投票榜 SSE 推送")
async def stream_board() -> StreamingResponse:
    async def events():  # noqa: ANN202
        last: str | None = None
        idle = 0
        while True:
            with SessionLocal() as db:
                event = company_service.current_event(db)
                payload = vote_service.board_payload(db, event)
            body = json.dumps(payload, ensure_ascii=False)
            if body != last:
                last = body
                yield f"event: board\ndata: {body}\n\n"
                idle = 0
            else:
                idle += 1
                if idle >= 8:  # 约 16 秒一次心跳，避免被代理断开
                    idle = 0
                    yield ": ping\n\n"
            await asyncio.sleep(2)

    return StreamingResponse(
        events(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )
