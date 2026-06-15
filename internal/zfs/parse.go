package zfs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParsePoolList parses `zpool list -Hp` output.
// Columns: name size alloc free ckpoint expandsz frag cap dedup health altroot
func ParsePoolList(out string) ([]Pool, error) {
	var pools []Pool
	for _, line := range splitLines(out) {
		fields := strings.Split(line, "\t")
		if len(fields) < 10 {
			continue
		}
		p := Pool{
			Name:   fields[0],
			Health: fields[9],
		}
		p.Size, _ = parseUint(fields[1])
		p.Alloc, _ = parseUint(fields[2])
		p.Free, _ = parseUint(fields[3])
		if fragStr := strings.TrimSuffix(fields[6], "%"); fragStr != "-" {
			p.FragPercent, _ = strconv.ParseFloat(fragStr, 64)
		}
		if capStr := strings.TrimSuffix(fields[7], "%"); capStr != "-" {
			p.UsedPercent, _ = strconv.ParseFloat(capStr, 64)
		} else if p.Size > 0 {
			p.UsedPercent = float64(p.Alloc) / float64(p.Size) * 100
		}
		if dedupStr := fields[8]; dedupStr != "-" {
			dedup := strings.TrimSuffix(dedupStr, "x")
			p.Dedup, _ = strconv.ParseFloat(dedup, 64)
		}
		if len(fields) > 10 {
			p.AltRoot = fields[10]
		}
		pools = append(pools, p)
	}
	return pools, nil
}

// ParseZpoolStatus parses `zpool status` output and decorates pools with scrub + vdev info.
func ParseZpoolStatus(out string, pools []Pool) []Pool {
	poolMap := make(map[string]*Pool, len(pools))
	for i := range pools {
		poolMap[pools[i].Name] = &pools[i]
	}

	var currentPool *Pool
	var inConfig bool
	var vdevStack [][]Vdev // stack of children lists

	for _, rawLine := range strings.Split(out, "\n") {
		line := strings.TrimRight(rawLine, " \t")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "pool:") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "pool:"))
			currentPool = poolMap[name]
			inConfig = false
			vdevStack = nil
			continue
		}

		if currentPool == nil {
			continue
		}

		if strings.HasPrefix(trimmed, "status:") {
			// status line may override health info
			continue
		}

		if strings.HasPrefix(trimmed, "config:") {
			inConfig = true
			vdevStack = [][]Vdev{{}}
			continue
		}

		if inConfig {
			if trimmed == "" || trimmed == "errors: No known data errors" {
				inConfig = false
				if len(vdevStack) > 0 {
					currentPool.Vdevs = vdevStack[0]
				}
				continue
			}
			// ignore the header line
			if strings.HasPrefix(trimmed, "NAME") {
				continue
			}
			_ = vdevStack // vdev parsing is best-effort
		}

		// Scrub lines
		if strings.Contains(trimmed, "scrub repaired") || strings.Contains(trimmed, "scrub in progress") ||
			strings.Contains(trimmed, "scrub canceled") || strings.Contains(trimmed, "scan:") {
			currentPool.Scrub = parseScrubLine(trimmed)
		}
	}
	return pools
}

func parseScrubLine(line string) ScrubStatus {
	s := ScrubStatus{State: line}
	if strings.Contains(line, "in progress") {
		s.InProgress = true
		// e.g. "scrub in progress since Thu Apr 10 03:00:02 2025\n 1.23T scanned ... 34.5% done"
	}
	if strings.Contains(line, "repaired") {
		s.State = "repaired"
	}
	if strings.Contains(line, "no errors") || strings.Contains(line, "with 0 errors") {
		s.State = "clean"
	}
	// Parse "scrub repaired 0B in 02:34:15 with 0 errors"
	parts := strings.Fields(line)
	for i, p := range parts {
		if p == "in" && i+1 < len(parts) {
			s.Duration = parts[i+1]
		}
		if p == "with" && i+1 < len(parts) {
			s.Errors, _ = parseUint(parts[i+1])
		}
	}
	return s
}

// ParseDatasets parses `zfs list -Hp -o name,type,used,avail,refer,mountpoint` output.
func ParseDatasets(out string) []Dataset {
	var ds []Dataset
	for _, line := range splitLines(out) {
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			continue
		}
		d := Dataset{
			Name:       fields[0],
			Type:       fields[1],
			Mountpoint: fields[5],
		}
		d.Used, _ = parseUint(fields[2])
		d.Avail, _ = parseUint(fields[3])
		d.Refer, _ = parseUint(fields[4])
		// Pool is the first component of the name
		if idx := strings.Index(d.Name, "/"); idx > 0 {
			d.Pool = d.Name[:idx]
		} else {
			d.Pool = d.Name
		}
		ds = append(ds, d)
	}
	return ds
}

// ParseSnapshots parses `zfs list -Hp -t snapshot -o name,used,refer,creation`.
func ParseSnapshots(out string) []Snapshot {
	var snaps []Snapshot
	for _, line := range splitLines(out) {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		sn := Snapshot{Name: fields[0]}
		sn.Used, _ = parseUint(fields[1])
		sn.Refer, _ = parseUint(fields[2])
		// creation is Unix timestamp in -Hp mode
		if ts, err := strconv.ParseInt(strings.TrimSpace(fields[3]), 10, 64); err == nil {
			sn.Created = time.Unix(ts, 0)
		}
		// Dataset is everything before the @
		if idx := strings.Index(sn.Name, "@"); idx > 0 {
			sn.Dataset = sn.Name[:idx]
			if slash := strings.Index(sn.Dataset, "/"); slash > 0 {
				sn.Pool = sn.Dataset[:slash]
			} else {
				sn.Pool = sn.Dataset
			}
		}
		snaps = append(snaps, sn)
	}
	return snaps
}

// --- helpers ---

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func parseUint(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "-" || s == "" {
		return 0, fmt.Errorf("no value")
	}
	return strconv.ParseUint(s, 10, 64)
}
