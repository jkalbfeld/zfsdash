package zfs

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/jkalbfeld/zfsdash/internal/config"
	"golang.org/x/crypto/ssh"
)

// SSHCollector runs zpool/zfs commands over SSH.
type SSHCollector struct {
	cfg    *config.SSHConfig
	client *ssh.Client
}

func NewSSHCollector(cfg *config.SSHConfig) *SSHCollector {
	return &SSHCollector{cfg: cfg}
}

func (c *SSHCollector) Name() string { return fmt.Sprintf("ssh:%s", c.cfg.Host) }

func (c *SSHCollector) Collect() (*CollectedData, error) {
	if err := c.ensureConnected(); err != nil {
		return nil, fmt.Errorf("ssh connect: %w", err)
	}

	poolListOut, err := c.run("zpool list -Hp")
	if err != nil {
		return nil, fmt.Errorf("zpool list: %w", err)
	}
	pools, err := ParsePoolList(poolListOut)
	if err != nil {
		return nil, err
	}

	statusOut, err := c.run("zpool status -v")
	if err == nil {
		pools = ParseZpoolStatus(statusOut, pools)
	}

	dsOut, err := c.run("zfs list -Hp -o name,type,used,avail,refer,mountpoint")
	var datasets []Dataset
	if err == nil {
		datasets = ParseDatasets(dsOut)
	}

	snOut, err := c.run("zfs list -Hp -t snapshot -o name,used,refer,creation -s creation")
	var snapshots []Snapshot
	if err == nil {
		snapshots = ParseSnapshots(snOut)
	}

	return &CollectedData{
		CollectedAt: time.Now(),
		Pools:       pools,
		Datasets:    datasets,
		Snapshots:   snapshots,
	}, nil
}

func (c *SSHCollector) ensureConnected() error {
	if c.client != nil {
		// ping with a no-op
		if _, err := c.run("true"); err == nil {
			return nil
		}
		c.client.Close()
		c.client = nil
	}

	auth, err := c.authMethod()
	if err != nil {
		return err
	}

	clientCfg := &ssh.ClientConfig{
		User:            c.cfg.User,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec — user-controlled config
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port))
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	c.client = client
	return nil
}

func (c *SSHCollector) authMethod() ([]ssh.AuthMethod, error) {
	if c.cfg.Password != "" {
		return []ssh.AuthMethod{ssh.Password(c.cfg.Password)}, nil
	}
	if c.cfg.KeyPath != "" {
		keyBytes, err := os.ReadFile(c.cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", c.cfg.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	return nil, fmt.Errorf("no auth method configured (set password or key_path)")
}

func (c *SSHCollector) run(cmd string) (string, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	var sb strings.Builder
	sess.Stdout = &sb
	if err := sess.Run(cmd); err != nil {
		return "", fmt.Errorf("run %q: %w", cmd, err)
	}
	return sb.String(), nil
}
