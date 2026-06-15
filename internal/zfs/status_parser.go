package zfs

import (
	"strconv"
	"strings"
	"time"
)

// parsePoolStatus parses the output of `zpool status -v <pool>`
// and extracts scrub info and vdev tree.
func parsePoolStatus(output string) (ScrubInfo, []VDev) {
	var scrub ScrubInfo
	var vdevs []VDev

	lines := strings.Split(output, "\n")
	inConfig := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Scrub parsing
		if strings.HasPrefix(trimmed, "scrub repaired") ||
			strings.HasPrefix(trimmed, "scrub in progress") ||
			strings.HasPrefix(trimmed, "scrub canceled") {
			scrub = parseScrubLine(trimmed)
		}

		// VDev tree starts after "config:" header
		if trimmed == "config:" {
			inConfig = true
			continue
		}
		if inConfig && trimmed == "errors:" {
			inConfig = false
			continue
		}
		// Skip the header line inside config block
		if inConfig && strings.HasPrefix(trimmed, "NAME") {
			continue
		}
		_ = i
	}

	// Parse vdev tree from config block
	vdevs = parseVDevTree(output)

	return scrub, vdevs
}

func parseScrubLine(line string) ScrubInfo {
	var s ScrubInfo

	if strings.Contains(line, "in progress") {
		s.InProgress = true
		s.Status = ScrubRunning
		// Try to extract percent: "scrub in progress since ... X.XX% done"
		if idx := strings.Index(line, "% done"); idx > 0 {
			start := strings.LastIndex(line[:idx], " ")
			if start >= 0 {
				s.Percent, _ = strconv.ParseFloat(line[start+1:idx], 64)
			}
		}
		return s
	}

	if strings.Contains(line, "repaired") {
		s.Status = ScrubDone
	}
	if strings.Contains(line, "canceled") {
		s.Status = ScrubCanceled
	}

	// "scrub repaired 0B in 00:01:23 with 0 errors on Sun Jan  1 00:00:00 2023"
	parts := strings.Fields(line)
	for i, p := range parts {
		switch p {
		case "repaired":
			if i+1 < len(parts) {
				s.Repaired = parts[i+1]
			}
		case "in":
			if i+1 < len(parts) && strings.Contains(parts[i+1], ":") {
				s.Duration = parts[i+1]
			}
		case "with":
			if i+1 < len(parts) {
				s.Errors, _ = strconv.ParseUint(parts[i+1], 10, 64)
			}
		case "on":
			// Rest is timestamp: "Sun Jan 1 00:00:00 2023"
			if i+1 < len(parts) {
				timeParts := strings.Join(parts[i+1:], " ")
				formats := []string{
					"Mon Jan  2 15:04:05 2006",
					"Mon Jan 2 15:04:05 2006",
				}
				for _, f := range formats {
					if t, err := time.Parse(f, timeParts); err == nil {
						s.LastRun = t
						break
					}
				}
			}
		}
	}
	return s
}

func parseVDevTree(output string) []VDev {
	var result []VDev

	// Find config block
	configStart := strings.Index(output, "\n\tNAME")
	if configStart < 0 {
		configStart = strings.Index(output, "\nNAME")
	}
	if configStart < 0 {
		return result
	}
	configEnd := strings.Index(output[configStart:], "\nerrors:")
	var configBlock string
	if configEnd > 0 {
		configBlock = output[configStart : configStart+configEnd]
	} else {
		configBlock = output[configStart:]
	}

	lines := strings.Split(configBlock, "\n")
	// Skip header (NAME STATE READ WRITE CKSUM)
	var vdevLines []string
	for _, l := range lines {
		if strings.Contains(l, "NAME") && strings.Contains(l, "STATE") {
			continue
		}
		if strings.TrimSpace(l) != "" {
			vdevLines = append(vdevLines, l)
		}
	}

	// Skip the pool name line (first)
	for _, l := range vdevLines[1:] {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		vdev := VDev{
			Name:   fields[0],
			Health: PoolHealth(fields[1]),
		}
		if len(fields) >= 5 {
			vdev.ReadErr, _ = strconv.ParseUint(fields[2], 10, 64)
			vdev.WriteErr, _ = strconv.ParseUint(fields[3], 10, 64)
			vdev.CkSumErr, _ = strconv.ParseUint(fields[4], 10, 64)
		}
		// Determine type by name prefix
		switch {
		case strings.HasPrefix(fields[0], "mirror"):
			vdev.Type = VDevMirror
		case strings.HasPrefix(fields[0], "raidz1"):
			vdev.Type = VDevRaidz1
		case strings.HasPrefix(fields[0], "raidz2"):
			vdev.Type = VDevRaidz2
		case strings.HasPrefix(fields[0], "raidz3"):
			vdev.Type = VDevRaidz3
		case strings.HasPrefix(fields[0], "spare"):
			vdev.Type = VDevSpare
		case strings.HasPrefix(fields[0], "log"):
			vdev.Type = VDevLog
		case strings.HasPrefix(fields[0], "cache"):
			vdev.Type = VDevCache
		default:
			vdev.Type = VDevDisk
		}
		result = append(result, vdev)
	}
	return result
}
