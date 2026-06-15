package zfs

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jkalbfeld/zfsdash/internal/config"
)

type TrueNASCollector struct {
	cfg    *config.TrueNASConfig
	client *http.Client
}

func NewTrueNASCollector(cfg *config.TrueNASConfig) *TrueNASCollector {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !cfg.TLSVerify},
	}
	return &TrueNASCollector{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second, Transport: tr},
	}
}

func (c *TrueNASCollector) Name() string {
	return "truenas://" + c.cfg.URL
}

func (c *TrueNASCollector) get(path string, out interface{}) error {
	req, err := http.NewRequest("GET", c.cfg.URL+"/api/v2.0/"+path, nil)
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
	if resp.StatusCode != 200 {
		return fmt.Errorf("truenas api %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// TrueNAS API response types
type tnPool struct {
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Size    uint64  `json:"size"`
	Alloced uint64  `json:"allocated"`
	Free    uint64  `json:"free"`
	Fragmentation string `json:"fragmentation"`
	Dedup   struct {
		Parsed float64 `json:"parsed"`
	} `json:"dedup"`
	Scan struct {
		State     string  `json:"state"`
		EndTime   *tnTime `json:"end_time"`
		Errors    uint64  `json:"errors"`
		BytesIssued uint64 `json:"bytes_issued"`
		BytesToProcess uint64 `json:"bytes_to_process"`
		TotalSecs float64 `json:"total_secs_left"`
	} `json:"scan"`
	Topology struct {
		Data []tnVDev `json:"data"`
		Log  []tnVDev `json:"log"`
		Spares []tnVDev `json:"spare"`
		Cache []tnVDev `json:"cache"`
	} `json:"topology"`
}

type tnTime struct {
	t time.Time
}

func (t *tnTime) UnmarshalJSON(b []byte) error {
	var v struct {
		Datetime string `json:"$date"`
	}
	if err := json.Unmarshal(b, &v); err == nil && v.Datetime != "" {
		parsed, err := time.Parse(time.RFC3339, v.Datetime)
		if err == nil {
			t.t = parsed
			return nil
		}
	}
	// numeric epoch ms
	var ms int64
	if err := json.Unmarshal(b, &ms); err == nil {
		t.t = time.Unix(ms/1000, 0)
		return nil
	}
	return nil
}

type tnVDev struct {
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Stats    tnStats  `json:"stats"`
	Children []tnVDev `json:"children"`
}

type tnStats struct {
	ReadErrors  uint64 `json:"read_errors"`
	WriteErrors uint64 `json:"write_errors"`
	ChecksumErrors uint64 `json:"checksum_errors"`
}

type tnDataset struct {
	Name      string  `json:"name"`
	Pool      string  `json:"pool"`
	Type      string  `json:"type"`
	Mountpoint string `json:"mountpoint"`
	Used struct {
		Parsed uint64 `json:"parsed"`
	} `json:"used"`
	Available struct {
		Parsed uint64 `json:"parsed"`
	} `json:"available"`
	Referenced struct {
		Parsed uint64 `json:"parsed"`
	} `json:"referenced"`
	UsedbySnapshots struct {
		Parsed uint64 `json:"parsed"`
	} `json:"usedbysnapshots"`
	Compressratio struct {
		Parsed float64 `json:"parsed"`
	} `json:"compressratio"`
}

type tnSnapshot struct {
	Name      string  `json:"name"`
	Pool      string  `json:"pool"`
	Dataset   string  `json:"dataset"`
	Properties struct {
		Creation struct {
			Parsed int64 `json:"parsed"`
		} `json:"creation"`
		Used struct {
			Parsed uint64 `json:"parsed"`
		} `json:"used"`
		Referenced struct {
			Parsed uint64 `json:"parsed"`
		} `json:"referenced"`
	} `json:"properties"`
}

func (c *TrueNASCollector) Collect() (*CollectedData, error) {
	data := &CollectedData{CollectedAt: time.Now()}

	var tnPools []tnPool
	if err := c.get("pool", &tnPools); err != nil {
		data.Error = err.Error()
		return data, err
	}
	for _, tp := range tnPools {
		p := Pool{
			Name:      tp.Name,
			Health:    PoolHealth(tp.Status),
			SizeBytes: tp.Size,
			UsedBytes: tp.Alloced,
			FreeBytes: tp.Free,
			Dedup:     tp.Dedup.Parsed,
		}
		p.FragPercent = parsePercent(tp.Fragmentation)
		if p.SizeBytes > 0 {
			p.UsedPercent = float64(p.UsedBytes) / float64(p.SizeBytes) * 100
		}
		// Scrub
		scan := tp.Scan
		if scan.State != "" {
			p.Scrub.InProgress = scan.State == "SCANNING"
			if p.Scrub.InProgress {
				p.Scrub.Status = ScrubRunning
				if scan.BytesToProcess > 0 {
					p.Scrub.Percent = float64(scan.BytesIssued) / float64(scan.BytesToProcess) * 100
				}
			} else {
				p.Scrub.Status = ScrubDone
			}
			p.Scrub.Errors = scan.Errors
			if scan.EndTime != nil {
				p.Scrub.LastRun = scan.EndTime.t
			}
		}
		// VDevs
		for _, tv := range tp.Topology.Data {
			p.VDevs = append(p.VDevs, convertVDev(tv))
		}
		data.Pools = append(data.Pools, p)
	}

	// Datasets
	var tnDatasets []tnDataset
	if err := c.get("pool/dataset", &tnDatasets); err == nil {
		for _, td := range tnDatasets {
			if td.Type == "SNAPSHOT" {
				continue
			}
			d := Dataset{
				Name:          td.Name,
				Pool:          td.Pool,
				Type:          strings.ToLower(td.Type),
				Mountpoint:    td.Mountpoint,
				UsedBytes:     td.Used.Parsed,
				AvailBytes:    td.Available.Parsed,
				ReferBytes:    td.Referenced.Parsed,
				UsedBySnaps:   td.UsedbySnapshots.Parsed,
				CompressRatio: td.Compressratio.Parsed,
			}
			data.Datasets = append(data.Datasets, d)
		}
	}

	// Snapshots
	var tnSnaps []tnSnapshot
	if err := c.get("zfs/snapshot", &tnSnaps); err == nil {
		for _, ts := range tnSnaps {
			s := Snapshot{
				Name:       ts.Name,
				Pool:       ts.Pool,
				Dataset:    ts.Dataset,
				UsedBytes:  ts.Properties.Used.Parsed,
				ReferBytes: ts.Properties.Referenced.Parsed,
				Created:    time.Unix(ts.Properties.Creation.Parsed, 0),
			}
			data.Snapshots = append(data.Snapshots, s)
		}
	}

	return data, nil
}

func convertVDev(tv tnVDev) VDev {
	v := VDev{
		Name:     tv.Name,
		Health:   PoolHealth(tv.Status),
		ReadErr:  tv.Stats.ReadErrors,
		WriteErr: tv.Stats.WriteErrors,
		CkSumErr: tv.Stats.ChecksumErrors,
	}
	switch strings.ToLower(tv.Type) {
	case "mirror":
		v.Type = VDevMirror
	case "raidz1":
		v.Type = VDevRaidz1
	case "raidz2":
		v.Type = VDevRaidz2
	case "raidz3":
		v.Type = VDevRaidz3
	default:
		v.Type = VDevDisk
	}
	for _, child := range tv.Children {
		v.Children = append(v.Children, convertVDev(child))
	}
	return v
}
