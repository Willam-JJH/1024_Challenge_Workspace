"""邮件驱动。

目前只有控制台驱动（把内容写日志），真实发信在部署时替换为 SMTP / Resend 实现，
接口保持不变：``send_code`` 与 ``send_notice``。
"""

import logging
from typing import Protocol

log = logging.getLogger("challenge-api.mail")


class Mailer(Protocol):
    def send_code(self, *, to: str, code: str, purpose: str, locale: str = "zh") -> None: ...

    def send_notice(self, *, to: str, subject: str, body: str) -> None: ...


class ConsoleMailer:
    """开发用：不真正发信，只写日志。"""

    def send_code(self, *, to: str, code: str, purpose: str, locale: str = "zh") -> None:
        log.info("验证码 to=%s purpose=%s code=%s locale=%s", to, purpose, code, locale)

    def send_notice(self, *, to: str, subject: str, body: str) -> None:
        log.info("通知 to=%s subject=%s body=%s", to, subject, body)


_mailer: Mailer = ConsoleMailer()


def get_mailer() -> Mailer:
    return _mailer


def set_mailer(mailer: Mailer) -> None:
    """测试或部署时替换驱动。"""
    global _mailer
    _mailer = mailer
