//go:build !unix

package collector

import "os"

func inodeOf(os.FileInfo) uint64 { return 0 }
