"""定时广播 worker：在应用生命周期内轮询到点的广播。

两个实现同时跑时靠 ``broadcasts.status`` 的条件更新认领，同一个广播只会被
一个进程发送。轮询间隔由 ``APP_BROADCAST_POLL_SECONDS`` 控制，0 表示关闭。
"""

import asyncio
import logging

from app.core.config import settings
from app.core.db import SessionLocal
from app.services.broadcasts import run_due

log = logging.getLogger("challenge-api.broadcast")


def tick() -> int:
    """处理一轮到点广播；异常只记日志，不让循环退出。"""
    with SessionLocal() as db:
        try:
            return run_due(db)
        except Exception:  # noqa: BLE001
            db.rollback()
            log.exception("定时广播轮询失败")
            return 0


async def run_forever() -> None:
    if settings.broadcast_poll_seconds <= 0:
        return
    interval = max(settings.broadcast_poll_seconds, 1)
    log.info("定时广播 worker 已启动，轮询间隔 %ss", interval)
    while True:
        await asyncio.to_thread(tick)
        await asyncio.sleep(interval)
