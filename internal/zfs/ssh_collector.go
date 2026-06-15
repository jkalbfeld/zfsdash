package zfs

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jkalbfeld/zfsdash/internal/config"
	"golang.org/x/crypto/ssh"
)

type SSHCollector struct {
	cfg *config.SSHConfig
}

func NewSSHCollector(cfg *config.SSHConfig) *SSHCollector {
	return &SSHCollector{cfg: cfg}
}

func (c *SSHCollector) Name() string {
	return fmt.Sprintf("ssh://%s@%s", c.cfg.User, c.cfg.Host)
}

func (c *SSHCollector) client() (*ssh.Client, error) {
	var authMethods []ssh.AuthMethod

	if c.cfg.KeyPath != "" {
		keyData, err := os.ReadFile(c.cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if c.cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(c.cfg.Password))
	}

	addr := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            c.cfg.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: known_hosts support
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	return client, nil
}

func (c *SSHCollector) run(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return strings.TrimSpace(string(out)), err
}

func (c *SSHCollector) Collect() (*CollectedData, error) {
	client, err := c.client()
	if err != nil {
		return &CollectedData{CollectedAt: time.Now(), Error: err.Error()}, err
	}
	defer client.Close()

	data := &CollectedData{CollectedAt: time.Now()}

	// Pools
	pools, err := c.collectPools(client)
	if err != nil {
		data.Error = err.Error()
		return data, err
	}
	data.Pools = pools

	// Datasets
	datasets, err := c.collectDatasets(client)
	if err == nil {
		data.Datasets = datasets
	}

	// Snapshots
	snapshots, err := c.collectSnapshots(client)
	if err == nil {
		data.Snapshots = snapshots
	}

	return data, nil
}

func (c *SSHCollector) collectPools(client *ssh.Client) ([]Pool, error) {
	// zpool list: name size alloc free frag dedup health altroot
	out, err := c.run(client, "zpool list -Hp -o name,size,alloc,free,frag,dedup,health")
	if err != nil {
		return nil, fmt.Errorf("zpool list: %w", err)
	}

	var pools []Pool
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		p := Pool{
			Name:      fields[0],
			SizeBytes: parseBytes(fields[1]),
			UsedBytes: parseBytes(fields[2]),
			FreeBytes: parseBytes(fields[3]),
			Health:    PoolHealth(fields[6]),
		}
		p.FragPercent = parsePercent(fields[4])
		p.Dedup = parseFloat(fields[5])
		if p.SizeBytes > 0 {
			p.UsedPercent = float64(p.UsedBytes) / float64(p.SizeBytes) * 100
		}

		// Get scrub info via zpool status
		scrub, vdevs := c.collectPoolStatus(client, p.Name)
		p.Scrub = scrub
		p.VDevs = vdevs

		pools = append(pools, p)
	}
	return pools, nil
}

func (c *SSHCollector) collectPoolStatus(client *ssh.Client, poolName string) (ScrubInfo, []VDev) {
	out, err := c.run(client, fmt.Sprintf("zpool status -v %s", poolName))
	if err != nil {
		return ScrubInfo{}, nil
	}
	return parsePoolStatus(out)
}

func (c *SSHCollector) collectDatasets(client *ssh.Client) ([]Dataset, error) {
	out, err := c.run(client,
		"zfs list -Hp -t filesystem,volume -o name,type,used,avail,refer,usedbysnapshots,mountpoint,compressratio")
	if err != nil {
		return nil, fmt.Errorf("zfs list: %w", err)
	}

	var datasets []Dataset
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		pool := strings.SplitN(fields[0], "/", 2)[0]
		d := Dataset{
			Name:          fields[0],
			Type:          fields[1],
			UsedBytes:     parseBytes(fields[2]),
			AvailBytes:    parseBytes(fields[3]),
			ReferBytes:    parseBytes(fields[4]),
			UsedBySnaps:   parseBytes(fields[5]),
			Mountpoint:    fields[6],
			CompressRatio: parseRatio(fields[7]),
			Pool:          pool,
		}
		datasets = append(datasets, d)
	}
	return datasets, nil
}

func (c *SSHCollector) collectSnapshots(client *ssh.Client) ([]Snapshot, error) {
	out, err := c.run(client,
		"zfs list -Hp -t snapshot -o name,used,refer,creation -s creation")
	if err != nil {
		return nil, fmt.Errorf("zfs snap list: %w", err)
	}

	var snaps []Snapshot
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// name is dataset@snapname
		parts := strings.SplitN(fields[0], "@", 2)
		dataset := parts[0]
		pool := strings.SplitN(dataset, "/", 2)[0]
		createdUnix, _ := strconv.ParseInt(fields[3], 10, 64)
		s := Snapshot{
			Name:       fields[0],
			Dataset:    dataset,
			UsedBytes:  parseBytes(fields[1]),
			ReferBytes: parseBytes(fields[2]),
			Created:    time.Unix(createdUnix, 0),
			Pool:       pool,
		}
		snaps = append(snaps, s)
	}
	return snaps, nil
}

// parseBytes converts zpool -Hp numeric byte string to uint64
func parseBytes(s string) uint64 {
	s = strings.TrimSuffix(s, "B")
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func parsePercent(s string) float64 {
	s = strings.TrimSuffix(s, "%")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseFloat(s string) float64 {
	// dedup can be "1.00x"
	s = strings.TrimSuffix(s, "x")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseRatio(s string) float64 {
	s = strings.TrimSuffix(s, "x")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
