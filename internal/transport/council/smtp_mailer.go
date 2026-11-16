package council

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type SMTPMailer struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
	Timeout  time.Duration
}

func (m SMTPMailer) Send(ctx context.Context, to, subject, text string) error {
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return fmt.Errorf("smtp sender address is invalid")
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("smtp recipient address is invalid")
	}
	if m.Host == "" || m.Port == "" || from.Address == "" || recipient.Address == "" {
		return fmt.Errorf("smtp configuration is incomplete")
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	address := net.JoinHostPort(m.Host, m.Port)
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("smtp dial failed")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	client, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake failed")
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return fmt.Errorf("smtp server does not offer STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: m.Host}); err != nil {
		return fmt.Errorf("smtp tls negotiation failed")
	}
	if m.Username != "" || m.Password != "" {
		if m.Username == "" || m.Password == "" {
			return fmt.Errorf("smtp credentials are incomplete")
		}
		if err := client.Auth(smtp.PlainAuth("", m.Username, m.Password, m.Host)); err != nil {
			return fmt.Errorf("smtp authentication failed")
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp sender rejected")
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return fmt.Errorf("smtp recipient rejected")
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp message rejected")
	}
	body := "From: " + from.String() + "\r\nTo: " + recipient.String() + "\r\nSubject: " + cleanHeader(subject) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + strings.ReplaceAll(text, "\n", "\r\n")
	if _, err := w.Write([]byte(body)); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp message write failed")
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp message delivery failed")
	}
	return client.Quit()
}

func cleanHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
