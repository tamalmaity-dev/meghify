package main

// hostSample is one instantaneous reading of the machine the panel runs on.
//
// CPU percent and the network counters are deltas: the first sample of a
// process lifetime has no predecessor, so its CPU share reads zero.
type hostSample struct {
	CPUPercent float64

	MemUsed   int64
	MemTotal  int64
	DiskUsed  int64
	DiskTotal int64

	// NetRx and NetTx are cumulative byte counters across non-loopback
	// interfaces; per-second rates are derived from consecutive samples.
	NetRx int64
	NetTx int64

	OS     string
	Kernel string
	CPUs   int
}
