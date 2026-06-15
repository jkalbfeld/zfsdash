package zfs

import "strings"

// ensure strings is used across truenas_collector.go
func init() { _ = strings.ToLower }
