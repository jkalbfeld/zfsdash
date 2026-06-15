package zfs

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jkalbfeld/zfsdash/internal/config"
)

// TrueNASCollector uses the TrueNAS Scale/Core REST API.
type TrueNASCollector struct {
	cfg    *config.TrueNASConfig
	client *http.Client
}

func NewTrueNASCollector(cfg *config.TrueNASConfig) *TrueNASCollector {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !cfg.TLSVerify}, //nolint:gosec
	}
	return &TrueNASCollector{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second, Transport: tr},
	}
}

func (c *TrueNASCollector) Name() string { return fmt.Sprintf("truenas:%s", c.cfg.URL) }

func (c *TrueNASCollector) Collect() (*CollectedData, error) {
	pools, err := c.fetchPools()
	if err != nil {
		return nil, err
	}
	datasets, err := c.fetchDatasets()
	if err != nil {
		return nil, err
	}
	snapshots, err := c.fetchSnapshots()
	if err != nil {
		return nil, err
	}
	return &CollectedData{
		CollectedAt: time.Now(),
		Pools:       pools,
		Datasets:    datasets,
		Snapshots:   snapshots,
	}, nil
}

// --- TrueNAS API shapes (subset we care about) ---

type tnPool struct {
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Size        uint64  `json:"size"`
	Allocated   uint64  `json:"allocated"`
	Free        uint64  `json:"free"`
	DedupRatio  float64 `json:"dedupratio"`
	FragPercent float64 `json:"fragmentation"`
	Scan        struct {
		Function       string  `json:"function"`
		State          string  `json:"state"`
		Errors         uint64  `json:"errors"`
		BytesIssued    uint64  `json:"bytes_issued"`
		BytesToProcess uint64  `json:"bytes_to_process"`
	} `json:"scan"`
}

type tnDataset struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Pool       string `json:"pool"`
	Mountpoint string `json:"mountpoint"`
	Used       struct{ Parsed uint64 `json:"parsed"` } `json:"used"`
	Available  struct{ Parsed uint64 `json:"parsed"` } `json:"available"`
	Referenced struct{ Parsed uint64 `json:"parsed"` } `json:"referenced"`
}

type tnSnapshot struct {
	ID         string `json:"id"`
	Dataset    string `json:"dataset"`
	Pool       string `json:"pool"`
	Used       struct{ Parsed uint64 `json:"parsed"` } `json:"used"`
	Referenced struct{ Parsed uint64 `json:"parsed"` } `json:"referenced"`
}

func (c *TrueNASCollector) fetchPools() ([]Pool, error) {
	var raw []tnPool
	if err := c.get("/api/v2.0/pool", &raw); err != nil {
		return nil, fmt.Errorf("fetch pools: %w", err)
	}
	out := make([]Pool, 0, len(raw))
	for _, r := range raw {
		p := Pool{
			Name:        r.Name,
			Health:      r.Status,
			Size:        r.Size,
			Alloc:       r.Allocated,
			Free:        r.Free,
			Dedup:       r.DedupRatio,
			FragPercent: r.FragPercent,
		}
		if p.Size > 0 {
			p.UsedPercent = float64(p.Alloc) / float64(p.Size) * 100
		}
		sc := ScrubStatus{}
		switch r.Scan.State {
		case "SCANNING":
			sc.InProgress = true
			sc.State = "in progress"
			if r.Scan.BytesToProcess > 0 {
				sc.PercentDone = float64(r.Scan.BytesIssued) / float64(r.Scan.BytesToProcess) * 100
			}
		case "FINISHED":
			sc.State = "clean"
			sc.Errors = r.Scan.Errors
			if r.Scan.Errors > 0 {
				sc.State = "errors"
			}
		}
		p.Scrub = sc
		out = append(out, p)
	}
	return out, nil
}

func (c *TrueNASCollector) fetchDatasets() ([]Dataset, error) {
	var raw []tnDataset
	if err := c.get("/api/v2.0/pool/dataset", &raw); err != nil {
		return nil, fmt.Errorf("fetch datasets: %w", err)
	}
	out := make([]Dataset, 0, len(raw))
	for _, r := range raw {
		out = append(out, Dataset{
			Name:       r.ID,
			Pool:       r.Pool,
			Type:       strings.ToLower(r.Type),
			Used:       r.Used.Parsed,
			Avail:      r.Available.Parsed,
			Refer:      r.Referenced.Parsed,
			Mountpoint: r.Mountpoint,
		})
	}
	return out, nil
}

func (c *TrueNASCollector) fetchSnapshots() ([]Snapshot, error) {
	var raw []tnSnapshot
	if err := c.get("/api/v2.0/zfs/snapshot", &raw); err != nil {
		return nil, fmt.Errorf("fetch snapshots: %w", err)
	}
	out := make([]Snapshot, 0, len(raw))
	for _, r := range raw {
		out = append(out, Snapshot{
			Name:    r.ID,
			Dataset: r.Dataset,
			Pool:    r.Pool,
			Used:    r.Used.Parsed,
			Refer:   r.Referenced.Parsed,
		})
	}
	return out, nil
}

func (c *TrueNASCollector) get(path string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, c.cfg.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("truenas api %s: %s — %s", path, resp.Status, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
