"""进程内滑动窗口限流。

计数只保存在本进程：单个容器内足够；多副本或灰度期两个实现同时在跑时，
每个进程各算一份（实际上限是配置值 × 进程数）。需要全局精确计数时应换成
Redis 或数据库计数，行为语义与 ``backend-go/internal/api/ratelimit.go`` 保持一致。
"""

import math
import threading
import time
from collections import deque

from app.core.config import settings
from app.core.errors import RateLimited

LOGIN_WINDOW = 15 * 60
CODE_WINDOW = 60 * 60
VERIFY_WINDOW = 60 * 60

_SWEEP_INTERVAL = 60.0
_STALE_AFTER = 3600.0
_SWEEP_THRESHOLD = 4096


class SlidingWindow:
    """按 key 记录事件时刻，窗口内事件数达到 limit 就拒绝。"""

    def __init__(self) -> None:
        self._events: dict[str, deque[float]] = {}
        self._lock = threading.Lock()
        self._last_sweep = time.monotonic()

    def allow(self, key: str, limit: int, window: float) -> tuple[bool, int]:
        """记录一次尝试，返回 (是否放行, 拒绝时建议等待的秒数)。"""
        if limit <= 0 or window <= 0:
            return True, 0
        now = time.monotonic()
        with self._lock:
            self._maybe_sweep(now)
            events = self._events.get(key)
            if events is None:
                events = deque()
                self._events[key] = events
            cutoff = now - window
            while events and events[0] <= cutoff:
                events.popleft()
            if len(events) >= limit:
                return False, max(math.ceil(events[0] + window - now), 1)
            events.append(now)
            return True, 0

    def reset(self) -> None:
        """清空计数（单元测试用）。"""
        with self._lock:
            self._events.clear()
            self._last_sweep = time.monotonic()

    def _maybe_sweep(self, now: float) -> None:
        if now - self._last_sweep < _SWEEP_INTERVAL and len(self._events) < _SWEEP_THRESHOLD:
            return
        self._last_sweep = now
        stale = now - _STALE_AFTER
        for key in [k for k, v in self._events.items() if not v or v[-1] <= stale]:
            self._events.pop(key, None)


limiter = SlidingWindow()


def request_limit(ip: str) -> None:
    """全域每 IP 请求速率。rate_limit_rps <= 0 时关闭。"""
    if not settings.rate_limit_enabled or settings.rate_limit_rps <= 0:
        return
    ok, retry = limiter.allow(f"rps:{ip}", settings.rate_limit_rps, 1.0)
    if not ok:
        raise RateLimited(retry_after=retry)


def record_login_failure(ip: str, email: str) -> None:
    """登录失败计数；超过阈值抛 429。成功登录不计。"""
    if not settings.rate_limit_enabled:
        return
    ok, retry = limiter.allow(f"login:ip:{ip}", settings.rate_limit_login_ip, LOGIN_WINDOW)
    if ok:
        ok, retry = limiter.allow(
            f"login:pair:{ip}|{email}", settings.rate_limit_login_pair, LOGIN_WINDOW
        )
    if not ok:
        raise RateLimited(retry_after=retry)


def limit_code_send(ip: str, email: str) -> None:
    """发验证码前的频控：两次间隔、每邮箱每小时、每 IP 每小时。"""
    if not settings.rate_limit_enabled:
        return
    ok, retry = limiter.allow(
        f"code:cooldown:{email}", 1, float(settings.rate_limit_code_cooldown)
    )
    if ok:
        ok, retry = limiter.allow(
            f"code:email:{email}", settings.rate_limit_code_email, CODE_WINDOW
        )
    if ok:
        ok, retry = limiter.allow(f"code:ip:{ip}", settings.rate_limit_code_ip, CODE_WINDOW)
    if not ok:
        raise RateLimited(retry_after=retry)


def limit_verify(email: str) -> None:
    """验证码校验次数上限，防止换着新码反复猜。"""
    if not settings.rate_limit_enabled:
        return
    ok, retry = limiter.allow(
        f"verify:email:{email}", settings.rate_limit_verify_email, VERIFY_WINDOW
    )
    if not ok:
        raise RateLimited(retry_after=retry)
