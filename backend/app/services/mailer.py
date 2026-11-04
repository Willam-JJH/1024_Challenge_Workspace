"""邮件驱动。

两种实现，接口保持一致（``send_code`` 与 ``send_notice``）：

- ``ConsoleMailer``：开发用，只写日志。
- ``SMTPMailer``：真实投递，配置走 ``APP_SMTP_*`` / ``APP_MAIL_*``。
  支持 465 隐式 TLS（``APP_SMTP_TLS=ssl``）、587 STARTTLS（``starttls``），
  以及本地假服务用的 ``none``。

发信失败统一抛 ``MailDeliveryError``，让调用方决定是失败重试（广播会逐条记
``failed``）还是把请求判为 500。
"""

import logging
import smtplib
import ssl
from email.message import EmailMessage
from email.utils import formataddr
from typing import Protocol

from app.core.config import settings

log = logging.getLogger("challenge-api.mail")

# 与 services/codes.py 的 CODE_TTL_MINUTES 保持一致（邮件正文里给用户看）
CODE_TTL_MINUTES = 15


class MailDeliveryError(RuntimeError):
    """SMTP 投递失败。"""


class Mailer(Protocol):
    def send_code(self, *, to: str, code: str, purpose: str, locale: str = "zh") -> None: ...

    def send_notice(self, *, to: str, subject: str, body: str) -> None: ...


class ConsoleMailer:
    """开发用：不真正发信，只写日志。"""

    def send_code(self, *, to: str, code: str, purpose: str, locale: str = "zh") -> None:
        log.info("验证码 to=%s purpose=%s code=%s locale=%s", to, purpose, code, locale)

    def send_notice(self, *, to: str, subject: str, body: str) -> None:
        log.info("通知 to=%s subject=%s body=%s", to, subject, body)


def code_message(*, code: str, purpose: str, locale: str = "zh") -> tuple[str, str]:
    """验证码邮件的主题与正文。"""
    minutes = CODE_TTL_MINUTES
    if locale.startswith("en"):
        if purpose == "password_reset":
            subject = "1024 Challenge password reset code"
            body = (
                f"You are resetting your password. Your verification code is {code}; "
                f"it expires in {minutes} minutes.\n\n"
                "If you did not request this, you can ignore this email."
            )
        else:
            subject = "1024 Challenge verification code"
            body = (
                f"Your verification code is {code}; it expires in {minutes} minutes.\n\n"
                "If you did not request this, you can ignore this email."
            )
        return subject, body

    if purpose == "password_reset":
        subject = "1024 Challenge 重置口令验证码"
        body = (
            f"你正在重置口令。验证码是 {code}，{minutes} 分钟内有效。\n\n"
            "如果不是你本人操作，请忽略这封邮件。"
        )
    else:
        subject = "1024 Challenge 验证码"
        body = (
            f"你的验证码是 {code}，{minutes} 分钟内有效。\n\n"
            "如果不是你本人操作，请忽略这封邮件。"
        )
    return subject, body


class SMTPMailer:
    """真实投递。一次连接发一封，失败抛 ``MailDeliveryError``。"""

    def __init__(
        self,
        *,
        host: str,
        port: int,
        user: str,
        password: str,
        tls: str,
        sender: str,
        sender_name: str = "",
        timeout: float = 10.0,
    ) -> None:
        if not host:
            raise RuntimeError("APP_MAIL_DRIVER=smtp 时必须设置 APP_SMTP_HOST")
        if not sender:
            raise RuntimeError("APP_MAIL_DRIVER=smtp 时必须设置 APP_MAIL_FROM")
        if tls not in {"ssl", "starttls", "none"}:
            raise RuntimeError(f"APP_SMTP_TLS 只能是 ssl/starttls/none，收到 {tls!r}")
        self.host = host
        self.port = port
        self.user = user
        # 从 .env 读到的值可能带行尾空格
        self.password = password.strip()
        self.tls = tls
        self.sender = sender
        self.sender_name = sender_name
        self.timeout = timeout

    def _connect(self) -> smtplib.SMTP:
        context = ssl.create_default_context()
        if self.tls == "ssl":
            return smtplib.SMTP_SSL(
                self.host, self.port, timeout=self.timeout, context=context
            )
        client = smtplib.SMTP(self.host, self.port, timeout=self.timeout)
        if self.tls == "starttls":
            client.ehlo()
            client.starttls(context=context)
        return client

    def _deliver(self, *, to: str, subject: str, body: str) -> None:
        message = EmailMessage()
        message["From"] = (
            formataddr((self.sender_name, self.sender))
            if self.sender_name
            else self.sender
        )
        message["To"] = to
        message["Subject"] = subject
        message.set_content(body)

        client: smtplib.SMTP | None = None
        try:
            client = self._connect()
            client.ehlo()
            if self.user:
                client.login(self.user, self.password)
            client.send_message(message)
        except (OSError, smtplib.SMTPException) as exc:
            raise MailDeliveryError(f"邮件发送失败：{exc}") from exc
        finally:
            if client is not None:
                try:
                    client.quit()
                except Exception:  # noqa: BLE001 - 连接已断时 quit 会再报错
                    client.close()

    def send_code(self, *, to: str, code: str, purpose: str, locale: str = "zh") -> None:
        subject, body = code_message(code=code, purpose=purpose, locale=locale)
        self._deliver(to=to, subject=subject, body=body)

    def send_notice(self, *, to: str, subject: str, body: str) -> None:
        self._deliver(to=to, subject=subject, body=body)


def build_mailer() -> Mailer:
    """按 ``APP_MAIL_DRIVER`` 选择驱动；配置不全时启动即失败。"""
    if settings.mail_driver == "smtp":
        return SMTPMailer(
            host=settings.smtp_host,
            port=settings.smtp_port,
            user=settings.smtp_user,
            password=settings.smtp_password,
            tls=settings.smtp_tls,
            sender=settings.mail_from,
            sender_name=settings.mail_from_name,
            timeout=settings.smtp_timeout,
        )
    if settings.mail_driver != "console":
        raise RuntimeError(f"未知的 APP_MAIL_DRIVER={settings.mail_driver!r}")
    return ConsoleMailer()


_mailer: Mailer = build_mailer()


def get_mailer() -> Mailer:
    return _mailer


def set_mailer(mailer: Mailer) -> None:
    """测试或部署时替换驱动。"""
    global _mailer
    _mailer = mailer
