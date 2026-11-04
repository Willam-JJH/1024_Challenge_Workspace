package auth

import "log/slog"

// Mailer 与 Python 的 services/mailer.py 接口一致；部署时替换成 SMTP/API 实现。
type Mailer interface {
	SendCode(to, code, purpose, locale string)
	SendNotice(to, subject, body string)
}

// LogMailer 是开发用驱动。与 Python 的 ConsoleMailer 不同，它**不打印验证码**，
// 只记录收件人与用途——验证码属于敏感凭据，不应该出现在日志里。
type LogMailer struct {
	Logger *slog.Logger
}

func (m LogMailer) SendCode(to, code, purpose, locale string) {
	m.Logger.Info("verification code issued",
		"to", to, "purpose", purpose, "locale", locale)
}

func (m LogMailer) SendNotice(to, subject, body string) {
	m.Logger.Info("notice", "to", to, "subject", subject)
}
