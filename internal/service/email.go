package service

import (
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"math/big"
	"net"
	"net/smtp"

	"domus/config"
)

// EmailSender abstracts email delivery so implementations can be swapped
// (SMTP, SendGrid, Mailgun, etc.) and mocked in tests.
type EmailSender interface {
	SendVerification(to, code string) error
	Configured() bool
}

// smtpEmailSender implements EmailSender over SMTP/STARTTLS.
type smtpEmailSender struct {
	cfg config.SMTPConfig
}

// NewSMTPEmailSender creates an EmailSender backed by SMTP.
func NewSMTPEmailSender(cfg config.SMTPConfig) EmailSender {
	return &smtpEmailSender{cfg: cfg}
}

func (s *smtpEmailSender) Configured() bool {
	return s.cfg.Host != "" && s.cfg.From != ""
}

func (s *smtpEmailSender) SendVerification(to, code string) error {
	subject := "Domus Verification Code"
	body := fmt.Sprintf("Your verification code is: %s\nValid for 5 minutes.", code)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.cfg.From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)

	if s.cfg.Port == 465 {
		return sendMailTLS(addr, auth, s.cfg.From, to, []byte(msg))
	}
	return smtp.SendMail(addr, auth, s.cfg.From, []string{to}, []byte(msg))
}

// GenerateEmailCode generates a 6-digit verification code using crypto/rand.
func GenerateEmailCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
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

// MockEmailSender is a test double for EmailSender.
type MockEmailSender struct {
	SendVerificationFn func(to, code string) error
	ConfiguredFn       func() bool
}

func (m *MockEmailSender) SendVerification(to, code string) error {
	if m.SendVerificationFn != nil {
		return m.SendVerificationFn(to, code)
	}
	return nil
}

func (m *MockEmailSender) Configured() bool {
	if m.ConfiguredFn != nil {
		return m.ConfiguredFn()
	}
	return false
}
