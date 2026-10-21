package claude

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var diagnosticPerformanceCounter = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
var diagnosticPerformanceFrequency = func() int64 {
	var frequency int64
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&frequency)))
	if ok == 0 || frequency <= 0 {
		panic("high-resolution test timer unavailable")
	}
	return frequency
}()

// Windows time.Since can be quantized; use QPC for loopback latency samples.
func diagnosticClockNS() int64 {
	var count int64
	ok, _, _ := diagnosticPerformanceCounter.Call(uintptr(unsafe.Pointer(&count)))
	if ok == 0 {
		panic("high-resolution test timer failed")
	}
	return count/diagnosticPerformanceFrequency*1e9 + count%diagnosticPerformanceFrequency*1e9/diagnosticPerformanceFrequency
}
