package service

import (
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"math/big"
	"net"
	"net/smtp"

	"zephyr/config"
)

// SmtpConfigured returns true if SMTP settings are present in the config.
func SmtpConfigured(cfg config.SMTPConfig) bool {
	return cfg.Host != "" && cfg.From != ""
}

// GenerateEmailCode generates a 6-digit verification code using crypto/rand.
func GenerateEmailCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

// SendVerificationEmail sends a verification code to the given email address.
// Supports port 465 (implicit TLS) and port 587 (STARTTLS).
func SendVerificationEmail(cfg config.SMTPConfig, to, code string) error {
	subject := "Zephyr Verification Code"
	body := fmt.Sprintf("Your verification code is: %s\nValid for 5 minutes.", code)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		cfg.From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)

	if cfg.Port == 465 {
		return sendMailTLS(addr, auth, cfg.From, to, []byte(msg))
	}
	// Port 587 or others: use STARTTLS via smtp.SendMail
	return smtp.SendMail(addr, auth, cfg.From, []string{to}, []byte(msg))
}

// sendMailTLS sends an email over implicit TLS (port 465).
func sendMailTLS(addr string, auth smtp.Auth, from, to string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("tls dial: %w", err)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}
