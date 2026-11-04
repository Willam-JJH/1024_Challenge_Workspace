package auth

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Mailer 与 Python 的 services/mailer.py 接口一致；失败必须返回 error，
// 让调用方决定回 5xx 还是把广播逐条记成 failed。
type Mailer interface {
	SendCode(to, code, purpose, locale string) error
	SendNotice(to, subject, body string) error
}

// LogMailer 是开发用驱动。与 Python 的 ConsoleMailer 不同，它**不打印验证码**，
// 只记录收件人与用途——验证码属于敏感凭据，不应该出现在日志里。
type LogMailer struct {
	Logger *slog.Logger
}

func (m LogMailer) SendCode(to, code, purpose, locale string) error {
	m.Logger.Info("verification code issued",
		"to", to, "purpose", purpose, "locale", locale)
	return nil
}

func (m LogMailer) SendNotice(to, subject, body string) error {
	m.Logger.Info("notice", "to", to, "subject", subject)
	return nil
}

// DriverName 与 Python 的 /admin/email 驱动标识保持一致：console / remote。
func DriverName(m Mailer) string {
	switch m.(type) {
	case LogMailer, *LogMailer:
		return "console"
	default:
		return "remote"
	}
}

// MailOptions 来自 APP_MAIL_* / APP_SMTP_*。
type MailOptions struct {
	Driver   string
	Host     string
	Port     int
	User     string
	Password string
	TLS      string // ssl（465 隐式 TLS）| starttls（587）| none
	From     string
	FromName string
	Timeout  time.Duration
}

// NewMailer 按驱动名构造；smtp 配置不全时返回 error，让服务启动即失败。
func NewMailer(opts MailOptions, logger *slog.Logger) (Mailer, error) {
	switch opts.Driver {
	case "", "console":
		return LogMailer{Logger: logger}, nil
	case "smtp":
		if opts.Host == "" {
			return nil, errors.New("APP_MAIL_DRIVER=smtp 时必须设置 APP_SMTP_HOST")
		}
		if opts.From == "" {
			return nil, errors.New("APP_MAIL_DRIVER=smtp 时必须设置 APP_MAIL_FROM")
		}
		if opts.TLS != "ssl" && opts.TLS != "starttls" && opts.TLS != "none" {
			return nil, fmt.Errorf("APP_SMTP_TLS 只能是 ssl/starttls/none，收到 %q", opts.TLS)
		}
		if opts.Timeout <= 0 {
			opts.Timeout = 10 * time.Second
		}
		// 从 .env 读到的值可能带行尾空格
		opts.Password = strings.TrimSpace(opts.Password)
		return &SMTPMailer{opts: opts}, nil
	default:
		return nil, fmt.Errorf("未知的 APP_MAIL_DRIVER=%q", opts.Driver)
	}
}

// SMTPMailer 每次投递建一条连接，失败返回 error。
type SMTPMailer struct {
	opts MailOptions
}

func (m *SMTPMailer) SendCode(to, code, purpose, locale string) error {
	subject, body := CodeMessage(code, purpose, locale)
	return m.send(to, subject, body)
}

func (m *SMTPMailer) SendNotice(to, subject, body string) error {
	return m.send(to, subject, body)
}

func (m *SMTPMailer) send(to, subject, body string) error {
	addr := net.JoinHostPort(m.opts.Host, strconv.Itoa(m.opts.Port))
	dialer := &net.Dialer{Timeout: m.opts.Timeout}

	var conn net.Conn
	var err error
	if m.opts.TLS == "ssl" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr,
			&tls.Config{ServerName: m.opts.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连接 SMTP 失败：%w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(m.opts.Timeout))

	client, err := smtp.NewClient(conn, m.opts.Host)
	if err != nil {
		return fmt.Errorf("SMTP 握手失败：%w", err)
	}
	defer client.Close()

	if m.opts.TLS == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 服务器不支持 STARTTLS")
		}
		if err := client.StartTLS(
			&tls.Config{ServerName: m.opts.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("STARTTLS 失败：%w", err)
		}
	}
	if m.opts.User != "" {
		auth := smtp.PlainAuth("", m.opts.User, m.opts.Password, m.opts.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败：%w", err)
		}
	}
	if err := client.Mail(m.opts.From); err != nil {
		return fmt.Errorf("SMTP MAIL FROM 失败：%w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO 失败：%w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA 失败：%w", err)
	}
	if _, err := writer.Write(composeMessage(m.opts.From, m.opts.FromName, to, subject, body)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("写邮件正文失败：%w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("提交邮件失败：%w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("SMTP QUIT 失败：%w", err)
	}
	return nil
}

// CodeMessage 与 Python 的 code_message 保持同样的文案。
func CodeMessage(code, purpose, locale string) (subject, body string) {
	const minutes = 15 // 与 services/codes.py 的 CODE_TTL_MINUTES 一致
	if strings.HasPrefix(locale, "en") {
		if purpose == "password_reset" {
			return "1024 Challenge password reset code", fmt.Sprintf(
				"You are resetting your password. Your verification code is %s; "+
					"it expires in %d minutes.\n\n"+
					"If you did not request this, you can ignore this email.", code, minutes)
		}
		return "1024 Challenge verification code", fmt.Sprintf(
			"Your verification code is %s; it expires in %d minutes.\n\n"+
				"If you did not request this, you can ignore this email.", code, minutes)
	}
	if purpose == "password_reset" {
		return "1024 Challenge 重置口令验证码", fmt.Sprintf(
			"你正在重置口令。验证码是 %s，%d 分钟内有效。\n\n"+
				"如果不是你本人操作，请忽略这封邮件。", code, minutes)
	}
	return "1024 Challenge 验证码", fmt.Sprintf(
		"你的验证码是 %s，%d 分钟内有效。\n\n"+
			"如果不是你本人操作，请忽略这封邮件。", code, minutes)
}

// composeMessage 生成 RFC 5322 报文：非 ASCII 主题用 RFC 2047 编码，正文 base64。
func composeMessage(from, fromName, to, subject, body string) []byte {
	fromHeader := from
	if fromName != "" {
		fromHeader = mime.QEncoding.Encode("utf-8", fromName) + " <" + from + ">"
	}
	var builder strings.Builder
	builder.WriteString("From: " + fromHeader + "\r\n")
	builder.WriteString("To: " + to + "\r\n")
	builder.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	builder.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	builder.WriteString("Content-Transfer-Encoding: base64\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(wrapBase64(body))
	builder.WriteString("\r\n")
	return []byte(builder.String())
}

func wrapBase64(text string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	var builder strings.Builder
	for len(encoded) > 76 {
		builder.WriteString(encoded[:76])
		builder.WriteString("\r\n")
		encoded = encoded[76:]
	}
	builder.WriteString(encoded)
	return builder.String()
}
