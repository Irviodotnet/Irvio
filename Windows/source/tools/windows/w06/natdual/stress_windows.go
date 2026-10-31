// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func (p *probe) stressNAT() map[string]any {
	result := map[string]any{"stage": "forward-stress", "success": false, "cycles": 0}
	handles := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	type sample struct {
		Cycle      int    `json:"cycle"`
		Handles    uint32 `json:"handles"`
		Goroutines int    `json:"goroutines"`
		Heap       uint64 `json:"heap_alloc"`
	}
	measure := func(cycle int) (sample, error) {
		runtime.GC()
		runtime.GC()
		var count uint32
		ret, _, err := handles.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
		if ret == 0 {
			return sample{}, err
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		return sample{cycle, count, runtime.NumGoroutine(), memory.HeapAlloc}, nil
	}
	for i := 0; i < 3; i++ {
		r := p.injectNAT()
		if r["responses"] != 5 || r["upstream_captures"] != 0 {
			result["error"] = fmt.Sprintf("warmup: %v", r)
			return result
		}
	}
	baseline, err := measure(0)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	samples := []sample{baseline}
	start := time.Now()
	for cycle := 1; cycle <= 100; cycle++ {
		r := p.injectNAT()
		if r["responses"] != 5 || r["upstream_captures"] != 0 {
			result["error"] = fmt.Sprintf("cycle %d: %v", cycle, r)
			break
		}
		result["cycles"] = cycle
		if cycle%10 == 0 {
			s, err := measure(cycle)
			if err != nil {
				result["error"] = err.Error()
				break
			}
			samples = append(samples, s)
		}
	}
	last := samples[len(samples)-1]
	result["samples"], result["elapsed_ms"] = samples, time.Since(start).Milliseconds()
	result["success"] = result["cycles"] == 100 && last.Handles <= baseline.Handles+4 && last.Goroutines <= baseline.Goroutines+4
	return result
}
