package zfs

import "fmt"

func init() {
	// ensure fmt is used (BytesToHuman uses it)
	_ = fmt.Sprintf
}
