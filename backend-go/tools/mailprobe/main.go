// Command mailprobe 用当前 APP_* 配置发一封测试邮件，部署后自检用。
//
//	go run ./tools/mailprobe -to you@example.com
//	echo "$APP_SMTP_PASSWORD" | go run ./tools/mailprobe -to you@example.com -password-stdin
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"challenge-api/internal/auth"
	"challenge-api/internal/config"
)

func main() {
	to := flag.String("to", "", "收件人地址")
	subject := flag.String("subject", "1024 Challenge 邮件配置自检", "邮件主题")
	passwordStdin := flag.Bool("password-stdin", false,
		"从标准输入读取 SMTP 密码，覆盖 APP_SMTP_PASSWORD")
	flag.Parse()

	if *to == "" {
		fmt.Fprintln(os.Stderr, "缺少 -to")
		os.Exit(2)
	}

	cfg := config.Load()
	password := cfg.SMTPPassword
	if *passwordStdin {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		password = strings.TrimRight(line, "\r\n")
	}

	mailer, err := auth.NewMailer(auth.MailOptions{
		Driver:   cfg.MailDriver,
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: password,
		TLS:      cfg.SMTPTLS,
		From:     cfg.MailFrom,
		FromName: cfg.MailFromName,
		Timeout:  cfg.SMTPTimeout,
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(1)
	}
	if auth.DriverName(mailer) == "console" {
		fmt.Fprintln(os.Stderr, "当前驱动是 console，不会真正发信；请设置 APP_MAIL_DRIVER=smtp")
		os.Exit(1)
	}

	if err := mailer.SendNotice(*to, *subject, "这是一封邮件配置自检，收到即表示 SMTP 可用。"); err != nil {
		fmt.Fprintln(os.Stderr, "发送失败:", err)
		os.Exit(1)
	}
	fmt.Println("send=ok")
}
