package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode                   string       `yaml:"mode"`
	SSH                    SSHConfig    `yaml:"ssh"`
	TrueNAS                TrueNASConfig `yaml:"truenas"`
	PollInterval           int          `yaml:"poll_interval"`
	CapacityAlertThreshold float64      `yaml:"capacity_alert_threshold"`
	AlertCooldownMinutes   int          `yaml:"alert_cooldown_minutes"`
	Email                  EmailConfig  `yaml:"email"`
	Webhooks               []Webhook    `yaml:"webhooks"`
	Listen                 string       `yaml:"listen"`
}

type SSHConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	KeyPath  string `yaml:"key_path"`
}

type TrueNASConfig struct {
	URL       string `yaml:"url"`
	APIKey    string `yaml:"api_key"`
	TLSVerify bool   `yaml:"tls_verify"`
}

type EmailConfig struct {
	Enabled      bool     `yaml:"enabled"`
	SMTPHost     string   `yaml:"smtp_host"`
	SMTPPort     int      `yaml:"smtp_port"`
	SMTPUser     string   `yaml:"smtp_user"`
	SMTPPassword string   `yaml:"smtp_password"`
	From         string   `yaml:"from"`
	To           []string `yaml:"to"`
}

type Webhook struct {
	URL string `yaml:"url"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	cfg := &Config{
		Mode:                   "ssh",
		PollInterval:           60,
		CapacityAlertThreshold: 85.0,
		AlertCooldownMinutes:   60,
		Listen:                 ":8080",
	}
	if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.SSH.Port == 0 {
		cfg.SSH.Port = 22
	}
	return cfg, nil
}
