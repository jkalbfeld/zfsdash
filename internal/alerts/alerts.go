package alerts

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/jkalbfeld/zfsdash/internal/config"
	"github.com/jkalbfeld/zfsdash/internal/zfs"
)

type alertKey struct {
	pool  string
	kind  string
}

type Manager struct {
	cfg      *config.Config
	mu       sync.Mutex
	lastSent map[alertKey]time.Time
}

func New(cfg *config.Config) *Manager {
	return &Manager{
		cfg:      cfg,
		lastSent: make(map[alertKey]time.Time),
	}
}

func (m *Manager) Evaluate(data *zfs.CollectedData) {
	for _, p := range data.Pools {
		if !p.Health.IsHealthy() {
			msg := fmt.Sprintf("ZFS pool '%s' is %s", p.Name, p.Health)
			m.send(alertKey{p.Name, "degraded"}, "[zfsdash] Pool Degraded: "+p.Name, msg)
		}
		if p.UsedPercent >= m.cfg.CapacityAlertThreshold {
			msg := fmt.Sprintf("ZFS pool '%s' is at %.1f%% capacity (threshold: %.0f%%)",
				p.Name, p.UsedPercent, m.cfg.CapacityAlertThreshold)
			m.send(alertKey{p.Name, "capacity"}, "[zfsdash] Pool Capacity Warning: "+p.Name, msg)
		}
	}
}

func (m *Manager) send(key alertKey, subject, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cooldown := time.Duration(m.cfg.AlertCooldownMinutes) * time.Minute
	if last, ok := m.lastSent[key]; ok && time.Since(last) < cooldown {
		return
	}
	m.lastSent[key] = time.Now()

	if m.cfg.Email.Enabled {
		if err := m.sendEmail(subject, body); err != nil {
			log.Printf("alert email error: %v", err)
		}
	}
	for _, wh := range m.cfg.Webhooks {
		if err := m.sendWebhook(wh.URL, subject, body); err != nil {
			log.Printf("alert webhook error (%s): %v", wh.URL, err)
		}
	}
}

func (m *Manager) sendEmail(subject, body string) error {
	e := m.cfg.Email
	addr := fmt.Sprintf("%s:%d", e.SMTPHost, e.SMTPPort)
	auth := smtp.PlainAuth("", e.SMTPUser, e.SMTPPassword, e.SMTPHost)

	msg := []byte(strings.Join([]string{
		"From: " + e.From,
		"To: " + strings.Join(e.To, ", "),
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n"))

	// Try STARTTLS first, fall back to plain
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: false})
	if err == nil {
		client, err := smtp.NewClient(conn, e.SMTPHost)
		if err != nil {
			return err
		}
		defer client.Close()
		if err := client.Auth(auth); err != nil {
			return err
		}
		if err := client.Mail(e.From); err != nil {
			return err
		}
		for _, to := range e.To {
			_ = client.Rcpt(to)
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		_, err = w.Write(msg)
		w.Close()
		return err
	}
	return smtp.SendMail(addr, auth, e.From, e.To, msg)
}

func (m *Manager) sendWebhook(url, subject, body string) error {
	payload := map[string]string{
		"text":    subject + "\n" + body,
		"subject": subject,
		"message": body,
	}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}
