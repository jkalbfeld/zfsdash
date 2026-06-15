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

// AlertManager fires email + webhook alerts with cooldown deduplication.
type AlertManager struct {
	cfg      *config.Config
	mu       sync.Mutex
	lastFired map[string]time.Time // key -> last fire time
}

func New(cfg *config.Config) *AlertManager {
	return &AlertManager{
		cfg:       cfg,
		lastFired: make(map[string]time.Time),
	}
}

// Evaluate checks the data for alert conditions and fires if needed.
func (am *AlertManager) Evaluate(data *zfs.CollectedData) {
	for _, pool := range data.Pools {
		// Degraded / faulted pool
		if pool.Health != "ONLINE" {
			key := fmt.Sprintf("pool-health:%s:%s", pool.Name, pool.Health)
			if am.shouldFire(key) {
				subject := fmt.Sprintf("[zfsdash] Pool %s is %s", pool.Name, pool.Health)
				body := fmt.Sprintf("ZFS pool '%s' health status: %s\nCapacity: %.1f%%\nCollected at: %s",
					pool.Name, pool.Health, pool.UsedPercent, data.CollectedAt.Format(time.RFC3339))
				am.fire(subject, body)
			}
		}

		// Capacity threshold
		if pool.UsedPercent >= am.cfg.CapacityAlertThreshold {
			key := fmt.Sprintf("pool-capacity:%s:%.0f", pool.Name, pool.UsedPercent)
			if am.shouldFire(key) {
				subject := fmt.Sprintf("[zfsdash] Pool %s at %.1f%% capacity", pool.Name, pool.UsedPercent)
				body := fmt.Sprintf("ZFS pool '%s' is %.1f%% full (threshold: %.1f%%)\nUsed: %s / %s\nCollected at: %s",
					pool.Name, pool.UsedPercent, am.cfg.CapacityAlertThreshold,
					formatBytes(pool.Alloc), formatBytes(pool.Size),
					data.CollectedAt.Format(time.RFC3339))
				am.fire(subject, body)
			}
		}
	}
}

func (am *AlertManager) shouldFire(key string) bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	cooldown := time.Duration(am.cfg.AlertCooldownMinutes) * time.Minute
	if last, ok := am.lastFired[key]; ok && time.Since(last) < cooldown {
		return false
	}
	am.lastFired[key] = time.Now()
	return true
}

func (am *AlertManager) fire(subject, body string) {
	log.Printf("ALERT: %s", subject)
	if am.cfg.Email.Enabled {
		go func() {
			if err := am.sendEmail(subject, body); err != nil {
				log.Printf("email alert error: %v", err)
			}
		}()
	}
	for _, wh := range am.cfg.Webhooks {
		wh := wh
		go func() {
			if err := am.sendWebhook(wh.URL, subject, body); err != nil {
				log.Printf("webhook alert error (%s): %v", wh.URL, err)
			}
		}()
	}
}

func (am *AlertManager) sendEmail(subject, body string) error {
	e := am.cfg.Email
	addr := fmt.Sprintf("%s:%d", e.SMTPHost, e.SMTPPort)
	auth := smtp.PlainAuth("", e.SMTPUser, e.SMTPPassword, e.SMTPHost)

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("From: %s\r\n", e.From))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(e.To, ", ")))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	// TLS
	tlsCfg := &tls.Config{ServerName: e.SMTPHost} //nolint:gosec
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		// Fallback to STARTTLS via smtp.SendMail
		return smtp.SendMail(addr, auth, e.From, e.To, []byte(msg.String()))
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, e.SMTPHost)
	if err != nil {
		return err
	}
	if err := client.Auth(auth); err != nil {
		return err
	}
	if err := client.Mail(e.From); err != nil {
		return err
	}
	for _, to := range e.To {
		if err := client.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = fmt.Fprint(w, msg.String())
	return err
}

type webhookPayload struct {
	Text    string `json:"text"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body,omitempty"`
}

func (am *AlertManager) sendWebhook(url, subject, body string) error {
	payload := webhookPayload{Text: fmt.Sprintf("%s\n%s", subject, body), Subject: subject, Body: body}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b)) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook %s: %s", url, resp.Status)
	}
	return nil
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
