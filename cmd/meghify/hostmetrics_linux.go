//go:build linux

package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// The CPU odometer needs the previous reading to turn two kernel counters into
// a busy percentage, so the last values live next to the code that reads them.
// A process restart simply costs one zeroed sample.
var (
	cpuMu        sync.Mutex
	prevCPUIdle  uint64
	prevCPUTotal uint64
)

// hostSampleNow reads CPU, memory, disk and network usage from the kernel.
func hostSampleNow() (hostSample, bool) {
	s := hostSample{CPUs: runtime.NumCPU()}
	s.OS, s.Kernel = hostFacts()

	if idle, total, ok := readCPUStat(); ok {
		cpuMu.Lock()
		if prevCPUTotal != 0 && total > prevCPUTotal {
			deltaTotal := total - prevCPUTotal
			deltaIdle := idle - prevCPUIdle
			s.CPUPercent = float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
		}
		prevCPUIdle, prevCPUTotal = idle, total
		cpuMu.Unlock()
	}

	if total, available, ok := readMemInfo(); ok {
		s.MemTotal = int64(total)
		s.MemUsed = int64(total - available)
	}

	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil && st.Bsize > 0 {
		s.DiskTotal = int64(st.Blocks * uint64(st.Bsize))
		s.DiskUsed = int64((st.Blocks - st.Bfree) * uint64(st.Bsize))
	}

	s.NetRx, s.NetTx = readNetDev()
	return s, true
}

// readCPUStat parses the aggregate "cpu" line of /proc/stat.
func readCPUStat() (idle, total uint64, ok bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	if !strings.HasPrefix(line, "cpu ") {
		return 0, 0, false
	}
	for i, field := range strings.Fields(line)[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += value
		// Fields 3 and 4 are idle and iowait; both count as not busy.
		if i == 3 || i == 4 {
			idle += value
		}
	}
	return idle, total, total > 0
}

// readMemInfo returns total and available bytes from /proc/meminfo.
func readMemInfo() (total, available uint64, ok bool) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = value * 1024
		case "MemAvailable:":
			available = value * 1024
		}
	}
	if total == 0 {
		return 0, 0, false
	}
	// Older kernels have no MemAvailable; fall back to reporting all RAM free
	// rather than inventing a used figure.
	if available == 0 || available > total {
		available = total
	}
	return total, available, true
}

// readNetDev sums the receive and transmit byte counters for every
// non-loopback interface.
func readNetDev() (rx, tx int64) {
	raw, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	lines := strings.Split(string(raw), "\n")
	// Two header lines first; interfaces follow as "name: rx... | tx...".
	for _, line := range lines[2:] {
		name, rest, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rxBytes, rxErr := strconv.ParseInt(fields[0], 10, 64)
		txBytes, txErr := strconv.ParseInt(fields[8], 10, 64)
		if rxErr != nil || txErr != nil {
			continue
		}
		rx += rxBytes
		tx += txBytes
	}
	return rx, tx
}

// hostFacts reads the OS name and kernel release once per process.
var (
	hostFactsOnce sync.Once
	hostOS        string
	hostKernel    string
)

func hostFacts() (string, string) {
	hostFactsOnce.Do(func() {
		if raw, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if name, value, found := strings.Cut(line, "="); found && name == "PRETTY_NAME" {
					hostOS = strings.Trim(value, `"`)
				}
			}
		}
		if raw, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			hostKernel = strings.TrimSpace(string(raw))
		}
	})
	return hostOS, hostKernel
}
