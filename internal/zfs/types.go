package zfs

import "time"

// CollectedData is the full snapshot returned by any Collector.
type CollectedData struct {
	CollectedAt time.Time
	Pools       []Pool
	Datasets    []Dataset
	Snapshots   []Snapshot
}

// Pool mirrors `zpool status` + `zpool list` output.
type Pool struct {
	Name        string
	Health      string  // ONLINE, DEGRADED, FAULTED, OFFLINE, UNAVAIL, REMOVED
	Size        uint64  // bytes
	Alloc       uint64  // bytes used
	Free        uint64  // bytes free
	UsedPercent float64 // 0–100
	Dedup       float64 // deduplication ratio (1.00 = no dedup)
	FragPercent float64
	AltRoot     string
	Scrub       ScrubStatus
	ScrubHistory []ScrubRecord
	Vdevs       []Vdev
}

// ScrubStatus is the last/current scrub.
type ScrubStatus struct {
	State       string    // scrub in progress / repaired / with no errors / canceled
	Started     time.Time
	Duration    string
	Repaired    uint64 // bytes
	Errors      uint64
	InProgress  bool
	PercentDone float64 // 0–100 when in progress
}

// ScrubRecord is a historical entry parsed from `zpool history`.
type ScrubRecord struct {
	Started  time.Time
	Finished time.Time
	Errors   uint64
	Repaired uint64
}

// Vdev is a vdev within a pool.
type Vdev struct {
	Name   string
	Type   string // mirror, raidz1, raidz2, disk, spare, cache, log
	Health string
	Read   uint64
	Write  uint64
	Cksum  uint64
	Children []Vdev
}

// Dataset mirrors `zfs list` output.
type Dataset struct {
	Name        string
	Pool        string
	Type        string // filesystem, volume, snapshot
	Used        uint64 // bytes
	Avail       uint64
	Refer       uint64
	Mountpoint  string
	// Growth tracking — populated by the store from history deltas
	Growth24h   int64 // bytes delta over last 24 h
}

// Snapshot mirrors `zfs list -t snapshot`.
type Snapshot struct {
	Name       string
	Dataset    string
	Pool       string
	Created    time.Time
	Used       uint64
	Refer      uint64
}

// Collector is the interface both SSH and TrueNAS collectors satisfy.
type Collector interface {
	Name() string
	Collect() (*CollectedData, error)
}
