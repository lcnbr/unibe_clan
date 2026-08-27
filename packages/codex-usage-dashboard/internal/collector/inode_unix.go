//go:build unix

package collector

import (
	"os"
	"syscall"
)

func inodeOf(info os.FileInfo) uint64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return stat.Ino
}
