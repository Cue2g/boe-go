// Package notify envía emails (API de Resend o SMTP, p. ej. Gmail con contraseña de aplicación).
package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Message es un email HTML.
type Message struct {
	To      string
	Subject string
	HTML    string
}

// Mailer envía mensajes.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// SMTPConfig configura el envío por SMTP.
type SMTPConfig struct {
	Host, User, Pass, From string
	Port                   int // 587 = STARTTLS, 465 = TLS implícito
}

// Config elige el método de envío: Resend si hay API key, si no SMTP si hay host.
type Config struct {
	Resend ResendConfig
	SMTP   SMTPConfig
}

// New devuelve un ResendMailer, un SMTPMailer o, si no hay nada configurado, un LogMailer.
func New(cfg Config) Mailer {
	if r := cfg.Resend; r.APIKey != "" {
		if r.From == "" {
			slog.Warn("SBOE_RESEND_FROM vacío: se usa onboarding@resend.dev (solo entrega a la cuenta dueña de la API key)")
			r.From = "onboarding@resend.dev"
		}
		return newResend(r)
	}
	c := cfg.SMTP
	if c.Host == "" || c.User == "" {
		slog.Warn("email no configurado (ni Resend ni SMTP): los emails solo se escribirán en el log")
		return LogMailer{}
	}
	if c.From == "" {
		c.From = c.User
	}
	if c.Port == 0 {
		c.Port = 587
	}
	return &SMTPMailer{cfg: c}
}

// SMTPMailer envía por SMTP autenticado.
type SMTPMailer struct{ cfg SMTPConfig }

func (s *SMTPMailer) Send(ctx context.Context, m Message) error {
	if strings.ContainsAny(m.To, "\r\n") || !strings.Contains(m.To, "@") {
		return fmt.Errorf("destinatario inválido: %q", m.To)
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	d := net.Dialer{Timeout: 30 * time.Second}
	var conn net.Conn
	var err error
	if s.cfg.Port == 465 {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: s.cfg.Host})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("conectando a %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(2 * time.Minute))
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if s.cfg.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("el servidor %s no soporta STARTTLS", s.cfg.Host)
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
			return err
		}
	}
	if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, s.cfg.Host)); err != nil {
		return fmt.Errorf("autenticación SMTP: %w", err)
	}
	if err := c.Mail(s.cfg.User); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(build(s.cfg.From, m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func build(from string, m Message) []byte {
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", mime.QEncoding.Encode("utf-8", "Alertas SBOE")+" <"+from+">")
	h("To", m.To)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("MIME-Version", "1.0")
	h("Content-Type", `text/html; charset="utf-8"`)
	h("Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(m.HTML))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return []byte(b.String())
}

// LogMailer solo escribe el email en el log (desarrollo).
type LogMailer struct{}

func (LogMailer) Send(_ context.Context, m Message) error {
	slog.Info("email (no enviado: email sin configurar)", "to", m.To, "subject", m.Subject, "bytes", len(m.HTML))
	return nil
}
