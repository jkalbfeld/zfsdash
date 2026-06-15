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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Defaults
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 60
	}
	if cfg.CapacityAlertThreshold == 0 {
		cfg.CapacityAlertThreshold = 85
	}
	if cfg.AlertCooldownMinutes == 0 {
		cfg.AlertCooldownMinutes = 60
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.SSH.Port == 0 {
		cfg.SSH.Port = 22
	}
	if cfg.Email.SMTPPort == 0 {
		cfg.Email.SMTPPort = 587
	}
	return &cfg, nil
}
