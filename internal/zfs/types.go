package zfs

import "time"

// Data is the full snapshot of ZFS state collected from the host.
type Data struct {
	Pools     []Pool
	Datasets  []Dataset
	Snapshots []Snapshot
	Errors    []string
}

// Pool represents a zpool.
type Pool struct {
	Name        string
	Health      string  // ONLINE, DEGRADED, FAULTED, OFFLINE, UNAVAIL, REMOVED
	SizeBytes   uint64
	UsedBytes   uint64
	FreeBytes   uint64
	UsedPct     float64
	Dedup       float64 // dedup ratio, e.g. 1.00
	FragPct     float64
	Scrub       ScrubStatus
	Vdevs       []Vdev
}

// ScrubStatus holds the latest scrub info for a pool.
type ScrubStatus struct {
	State      string // scrub in progress / scrub repaired / none requested
	EndTime    time.Time
	Duration   string
	Repaired   uint64
	Errors     uint64
	InProgress bool
	PctDone    float64
}

// Vdev is a single device or mirror/raidz group inside a pool.
type Vdev struct {
	Name   string
	Type   string // disk, mirror, raidz, spare, cache, log
	Health string
	Read   uint64
	Write  uint64
	Cksum  uint64
}

// Dataset represents a ZFS filesystem or volume.
type Dataset struct {
	Name        string
	Kind        string // filesystem, volume, snapshot
	UsedBytes   uint64
	AvailBytes  uint64
	ReferBytes  uint64
	CompRatio   float64
	Mountpoint  string
	Origin      string // for clones
	Pool        string
}

// Snapshot represents a ZFS snapshot.
type Snapshot struct {
	Name       string
	Dataset    string
	Created    time.Time
	UsedBytes  uint64
	ReferBytes uint64
	Pool       string
}

// Collector is the interface both SSH and TrueNAS collectors satisfy.
type Collector interface {
	Collect() (*Data, error)
	Name() string
}
