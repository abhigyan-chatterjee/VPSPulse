package hub

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"vpspulse/internal/store"
)

func sendSMTP(ctx context.Context, ch store.NotificationChannel, category, severity, title, detail string) error {
	host := strings.TrimSpace(ch.Config["host"])
	port := strings.TrimSpace(ch.Config["port"])
	if port == "" {
		port = "587"
	}
	from := sanitizeHeader(ch.Config["from"])
	rawTo := strings.TrimSpace(ch.Config["to"])
	username := strings.TrimSpace(ch.Config["username"])
	password := ch.Config["password"]
	tlsMode := strings.ToLower(strings.TrimSpace(ch.Config["tls_mode"]))
	if tlsMode == "" {
		tlsMode = "auto"
	}

	if host == "" || from == "" || rawTo == "" {
		return fmt.Errorf("smtp requires host, from, and to")
	}

	var recipients []string
	for _, r := range strings.Split(rawTo, ",") {
		addr := sanitizeHeader(r)
		if addr != "" {
			recipients = append(recipients, addr)
		}
	}
	if len(recipients) == 0 {
		return fmt.Errorf("smtp requires at least one valid recipient")
	}

	addr := net.JoinHostPort(host, port)
	deadline := time.Now().Add(10 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	dialer := &net.Dialer{Timeout: 8 * time.Second}
	tlsCfg := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}

	useImplicitTLS := tlsMode == "tls" || (tlsMode == "auto" && port == "465")

	var conn net.Conn
	var err error
	if useImplicitTLS {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: tlsCfg}
		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if !useImplicitTLS && (tlsMode == "starttls" || tlsMode == "auto") {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		} else if tlsMode == "starttls" {
			return fmt.Errorf("smtp server does not advertise STARTTLS")
		}
	}

	if username != "" || password != "" {
		auth := smtp.PlainAuth("", username, password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", rcpt, err)
		}
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}

	cleanTitle := sanitizeHeader(title)
	subject := fmt.Sprintf("[VPSPulse] [%s] %s", strings.ToUpper(sanitizeHeader(severity)), cleanTitle)
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + strings.Join(recipients, ", "),
		"Subject: " + subject,
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		`Content-Type: text/plain; charset="utf-8"`,
		"",
		fmt.Sprintf("Severity: %s", severity),
		fmt.Sprintf("Category: %s", category),
		fmt.Sprintf("Title:    %s", cleanTitle),
		fmt.Sprintf("Detail:   %s", detail),
		fmt.Sprintf("Time:     %s", time.Now().UTC().Format(time.RFC3339)),
		"",
	}, "\r\n")

	if _, err := wc.Write([]byte(msg)); err != nil {
		_ = wc.Close()
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return strings.TrimSpace(s)
}
