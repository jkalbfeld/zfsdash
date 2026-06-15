package zfs

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jkalbfeld/zfsdash/internal/config"
	"golang.org/x/crypto/ssh"
)

// SSHCollector connects over SSH and runs zpool/zfs commands.
type SSHCollector struct {
	cfg    *config.SSHConfig
	client *ssh.Client
}

func NewSSHCollector(cfg *config.SSHConfig) *SSHCollector {
	return &SSHCollector{cfg: cfg}
}

func (c *SSHCollector) Name() string { return "ssh" }

func (c *SSHCollector) connect() error {
	if c.client != nil {
		// test existing connection
		if _, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			return nil
		}
		c.client.Close()
		c.client = nil
	}

	var authMethods []ssh.AuthMethod

	if c.cfg.KeyPath != "" {
		key, err := os.ReadFile(c.cfg.KeyPath)
		if err != nil {
			return fmt.Errorf("read key %q: %w", c.cfg.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return fmt.Errorf("parse key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if c.cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(c.cfg.Password))
	}

	if len(authMethods) == 0 {
		return fmt.Errorf("ssh: no auth method configured (set password or key_path)")
	}

	addr := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            c.cfg.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec — user-managed env
		Timeout:         15 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	c.client = client
	return nil
}

func (c *SSHCollector) run(cmd string) (string, error) {
	if err := c.connect(); err != nil {
		return "", err
	}
	sess, err := c.client.NewSession()
	if err != nil {
		c.client = nil
		return "", fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	var buf bytes.Buffer
	sess.Stdout = &buf
	sess.Stderr = io.Discard
	if err := sess.Run(cmd); err != nil {
		// some zfs commands exit non-zero on partial output; return what we got
		log.Printf("ssh cmd %q: %v", cmd, err)
	}
	return buf.String(), nil
}

func (c *SSHCollector) Collect() (*Data, error) {
	data := &Data{}

	// ---- zpool list ----
	poolOut, err := c.run(`zpool list -H -o name,health,size,alloc,free,frag,dedup`)
	if err != nil {
		return nil, fmt.Errorf("zpool list: %w", err)
	}

	pools := map[string]*Pool{}
	for _, line := range splitLines(poolOut) {
		f := splitFields(line, 7)
		if f == nil {
			continue
		}
		p := Pool{
			Name:      f[0],
			Health:    f[1],
			SizeBytes: parseSize(f[2]),
			UsedBytes: parseSize(f[3]),
			FreeBytes: parseSize(f[4]),
			FragPct:   parsePct(f[5]),
			Dedup:     parseDedupRatio(f[6]),
		}
		if p.SizeBytes > 0 {
			p.UsedPct = float64(p.UsedBytes) / float64(p.SizeBytes) * 100
		}
		pools[p.Name] = &p
	}

	// ---- zpool status (scrub + vdevs) ----
	for name, pool := range pools {
		statusOut, err := c.run(fmt.Sprintf("zpool status -v %s", name))
		if err == nil {
			pool.Scrub = parseScrub(statusOut)
			pool.Vdevs = parseVdevs(statusOut)
		}
		data.Pools = append(data.Pools, *pool)
	}

	// ---- zfs list (datasets) ----
	dsOut, err := c.run(`zfs list -H -o name,type,used,avail,refer,compressratio,mountpoint -t filesystem,volume`)
	if err == nil {
		for _, line := range splitLines(dsOut) {
			f := splitFields(line, 7)
			if f == nil {
				continue
			}
			ds := Dataset{
				Name:       f[0],
				Kind:       f[1],
				UsedBytes:  parseSize(f[2]),
				AvailBytes: parseSize(f[3]),
				ReferBytes: parseSize(f[4]),
				CompRatio:  parseCompRatio(f[5]),
				Mountpoint: f[6],
				Pool:       poolFromName(f[0]),
			}
			data.Datasets = append(data.Datasets, ds)
		}
	}

	// ---- zfs list snapshots ----
	snapOut, err := c.run(`zfs list -H -o name,creation,used,refer -t snapshot -s creation`)
	if err == nil {
		for _, line := range splitLines(snapOut) {
			f := splitFields(line, 4)
			if f == nil {
				continue
			}
			dataset, _, _ := strings.Cut(f[0], "@")
			sn := Snapshot{
				Name:       f[0],
				Dataset:    dataset,
				Created:    parseCreation(f[1]),
				UsedBytes:  parseSize(f[2]),
				ReferBytes: parseSize(f[3]),
				Pool:       poolFromName(f[0]),
			}
			data.Snapshots = append(data.Snapshots, sn)
		}
	}

	return data, nil
}

// ---- parsing helpers ----

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitFields(line string, n int) []string {
	f := strings.Fields(line)
	if len(f) < n {
		return nil
	}
	return f
}

// parseSize converts zpool human-readable sizes (1.23T, 456G, etc.) to bytes.
func parseSize(s string) uint64 {
	if s == "-" || s == "" {
		return 0
	}
	s = strings.ToUpper(strings.TrimSpace(s))
	multipliers := map[byte]uint64{
		'K': 1024,
		'M': 1024 * 1024,
		'G': 1024 * 1024 * 1024,
		'T': 1024 * 1024 * 1024 * 1024,
		'P': 1024 * 1024 * 1024 * 1024 * 1024,
	}
	last := s[len(s)-1]
	if mult, ok := multipliers[last]; ok {
		v, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil {
			return 0
		}
		return uint64(v * float64(mult))
	}
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func parsePct(s string) float64 {
	s = strings.TrimSuffix(s, "%")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseDedupRatio(s string) float64 {
	s = strings.TrimSuffix(s, "x")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseCompRatio(s string) float64 {
	s = strings.TrimSuffix(s, "x")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseCreation(s string) time.Time {
	// zfs outputs creation as Unix timestamp with -p, or human date otherwise.
	// We use the human form here; parse best-effort.
	t, err := time.Parse("Mon Jan _2 15:04 2006", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func poolFromName(name string) string {
	parts := strings.SplitN(name, "/", 2)
	return parts[0]
}

// parseScrub extracts scrub status from `zpool status` output.
func parseScrub(status string) ScrubStatus {
	var sc ScrubStatus
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "scan:") {
			sc.State = strings.TrimPrefix(line, "scan:")
			sc.State = strings.TrimSpace(sc.State)
			if strings.Contains(sc.State, "in progress") {
				sc.InProgress = true
			}
		}
		// Errors line
		if strings.Contains(line, "repaired") && strings.Contains(line, "errors") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "repaired" && i > 0 {
					sc.Repaired, _ = strconv.ParseUint(parts[i-1], 10, 64)
				}
				if p == "errors" && i > 0 {
					v := strings.TrimSuffix(parts[i-1], "B")
					sc.Errors, _ = strconv.ParseUint(v, 10, 64)
				}
			}
		}
	}
	return sc
}

// parseVdevs extracts a simple flat list of vdevs from `zpool status` output.
func parseVdevs(status string) []Vdev {
	var vdevs []Vdev
	inConfig := false
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "config:" {
			inConfig = true
			continue
		}
		if !inConfig {
			continue
		}
		// End of config block
		if strings.TrimSpace(line) == "errors:" || strings.HasPrefix(strings.TrimSpace(line), "errors:") {
			break
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name := f[0]
		health := f[1]
		if name == "NAME" {
			continue
		}
		vdev := Vdev{Name: name, Health: health}
		if len(f) >= 5 {
			vdev.Read, _ = strconv.ParseUint(f[2], 10, 64)
			vdev.Write, _ = strconv.ParseUint(f[3], 10, 64)
			vdev.Cksum, _ = strconv.ParseUint(f[4], 10, 64)
		}
		vdevs = append(vdevs, vdev)
	}
	return vdevs
}
