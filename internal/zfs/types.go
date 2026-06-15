package zfs

import "time"

// PoolHealth represents the health state of a ZFS pool
type PoolHealth string

const (
	HealthOnline   PoolHealth = "ONLINE"
	HealthDegraded PoolHealth = "DEGRADED"
	HealthFaulted  PoolHealth = "FAULTED"
	HealthOffline  PoolHealth = "OFFLINE"
	HealthUnavail  PoolHealth = "UNAVAIL"
	HealthRemoved  PoolHealth = "REMOVED"
)

func (h PoolHealth) IsHealthy() bool {
	return h == HealthOnline
}

func (h PoolHealth) CSSClass() string {
	switch h {
	case HealthOnline:
		return "status-ok"
	case HealthDegraded:
		return "status-warn"
	case HealthFaulted, HealthUnavail:
		return "status-err"
	default:
		return "status-unknown"
	}
}

type Pool struct {
	Name        string     `json:"name"`
	Health      PoolHealth `json:"health"`
	SizeBytes   uint64     `json:"size_bytes"`
	UsedBytes   uint64     `json:"used_bytes"`
	FreeBytes   uint64     `json:"free_bytes"`
	UsedPercent float64    `json:"used_percent"`
	Dedup       float64    `json:"dedup"`
	FragPercent float64    `json:"frag_percent"`
	Scrub       ScrubInfo  `json:"scrub"`
	VDevs       []VDev     `json:"vdevs"`
	Errors      string     `json:"errors"`
}

type ScrubStatus string

const (
	ScrubNone    ScrubStatus = "none"
	ScrubRunning ScrubStatus = "scrub in progress"
	ScrubDone    ScrubStatus = "scrub repaired"
	ScrubCanceled ScrubStatus = "scrub canceled"
)

type ScrubInfo struct {
	Status     ScrubStatus `json:"status"`
	LastRun    time.Time   `json:"last_run"`
	Duration   string      `json:"duration"`
	Repaired   string      `json:"repaired"`
	Errors     uint64      `json:"errors"`
	InProgress bool        `json:"in_progress"`
	Percent    float64     `json:"percent"`
}

type VDevType string

const (
	VDevDisk   VDevType = "disk"
	VDevMirror VDevType = "mirror"
	VDevRaidz1 VDevType = "raidz1"
	VDevRaidz2 VDevType = "raidz2"
	VDevRaidz3 VDevType = "raidz3"
	VDevSpare  VDevType = "spare"
	VDevLog    VDevType = "log"
	VDevCache  VDevType = "cache"
)

type VDev struct {
	Name     string     `json:"name"`
	Type     VDevType   `json:"type"`
	Health   PoolHealth `json:"health"`
	Children []VDev     `json:"children,omitempty"`
	ReadErr  uint64     `json:"read_err"`
	WriteErr uint64     `json:"write_err"`
	CkSumErr uint64     `json:"cksum_err"`
}

type Dataset struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"` // filesystem, volume, snapshot
	UsedBytes   uint64  `json:"used_bytes"`
	AvailBytes  uint64  `json:"avail_bytes"`
	ReferBytes  uint64  `json:"refer_bytes"`
	UsedBySnaps uint64  `json:"used_by_snaps"`
	Mountpoint  string  `json:"mountpoint"`
	CompressRatio float64 `json:"compress_ratio"`
	Pool        string  `json:"pool"`
}

type Snapshot struct {
	Name      string    `json:"name"`
	Dataset   string    `json:"dataset"`
	Created   time.Time `json:"created"`
	UsedBytes uint64    `json:"used_bytes"`
	ReferBytes uint64   `json:"refer_bytes"`
	Pool      string    `json:"pool"`
}

type CollectedData struct {
	CollectedAt time.Time  `json:"collected_at"`
	Pools       []Pool     `json:"pools"`
	Datasets    []Dataset  `json:"datasets"`
	Snapshots   []Snapshot `json:"snapshots"`
	Error       string     `json:"error,omitempty"`
}

// Collector is the interface both SSH and TrueNAS backends implement
type Collector interface {
	Collect() (*CollectedData, error)
	Name() string
}

// BytesToHuman converts bytes to a human-readable string
func BytesToHuman(b uint64) string {
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
