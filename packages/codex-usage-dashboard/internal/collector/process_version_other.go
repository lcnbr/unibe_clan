//go:build !linux

package collector

import "context"

func defaultLiveCodexVersionDetector(map[string]string) func(context.Context) string {
	return func(context.Context) string { return "" }
}
