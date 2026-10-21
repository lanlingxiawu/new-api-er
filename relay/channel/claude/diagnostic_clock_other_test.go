//go:build !windows

package claude

import "time"

var diagnosticClockOrigin = time.Now()

func diagnosticClockNS() int64 {
	return time.Since(diagnosticClockOrigin).Nanoseconds()
}
