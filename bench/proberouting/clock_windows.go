package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var performanceCounter = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
var performanceFrequency = func() int64 {
	var frequency int64
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&frequency)))
	if ok == 0 || frequency <= 0 {
		panic("high-resolution timer unavailable")
	}
	return frequency
}()

// Windows time.Since can be quantized on some hosts. Use QPC so sub-millisecond
// loopback requests do not incorrectly appear as zero-duration samples.
func clockNS() int64 {
	var count int64
	ok, _, _ := performanceCounter.Call(uintptr(unsafe.Pointer(&count)))
	if ok == 0 {
		panic("high-resolution timer failed")
	}
	return count/performanceFrequency*1e9 + count%performanceFrequency*1e9/performanceFrequency
}
