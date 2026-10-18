package deploy

import (
	"math"
	"strings"
	"testing"
)

func TestParseHumanSize(t *testing.T) {
	// The CLI's decimal sizes go through a float; allow a byte of rounding.
	cases := map[string]float64{
		"0B":      0,
		"127kB":   127_000,
		"4.559GB": 4_559_000_000,
		"32.5MB":  32_500_000,
		"1.592GB": 1_592_000_000,
		"695.8MB": 695_800_000,
		"15kB":    15_000,
		"1.037GB": 1_037_000_000,
		"":        0,
		"garbage": 0,
	}
	for input, want := range cases {
		if got := parseHumanSize(input); math.Abs(float64(got)-want) > 1 {
			t.Errorf("parseHumanSize(%q) = %d, want %d", input, got, int64(want))
		}
	}
}

func TestParseReclaimedSpace(t *testing.T) {
	jsonOut := `{"AmountOfDiskReclaimed":0}{"SpaceReclaimed": 123456}`
	if got := parseReclaimedSpace(jsonOut); got != 123456 {
		t.Errorf("json reclaimed = %d, want 123456", got)
	}

	textOut := "ID RECLAIMABLE SIZE\n" +
		"9zbkqj79duhaaafozw1m7dkhs\ttrue\t\t512B\t1.106MB\n" +
		"Total reclaimed space: 32.5MB\n"
	if got := parseReclaimedSpace(textOut); got != 32_500_000 {
		t.Errorf("text reclaimed = %d, want 32500000", got)
	}

	if got := parseReclaimedSpace("nothing to reclaim"); got != 0 {
		t.Errorf("empty reclaimed = %d, want 0", got)
	}
}

// The `docker system df --format json` rows map onto the sandbox usage
// struct; a missing Build Cache row (engines hide it when empty) stays zero.
func TestSandboxDiskUsageRecords(t *testing.T) {
	out := strings.Join([]string{
		`{"Active":"4","Reclaimable":"695.8MB (15%)","Size":"4.559GB","TotalCount":"38","Type":"Images"}`,
		`{"Active":"3","Reclaimable":"0B (0%)","Size":"127kB","TotalCount":"3","Type":"Containers"}`,
		`{"Active":"2","Reclaimable":"79.87MB (5%)","Size":"1.592GB","TotalCount":"5","Type":"Local Volumes"}`,
		`{"Active":"0","Reclaimable":"0B (0%)","Size":"0B","TotalCount":"0","Type":"Build Cache"}`,
	}, "\n")

	records := scanJSONLines(out, sandboxDiskUsageRecord{})
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4", len(records))
	}

	var usage SandboxUsage
	for _, record := range records {
		switch record.Type {
		case "Images":
			usage.Images = parseHumanSize(record.Size)
		case "Containers":
			usage.Containers = parseHumanSize(record.Size)
		case "Local Volumes":
			usage.Volumes = parseHumanSize(record.Size)
		case "Build Cache":
			usage.BuildCache = parseHumanSize(record.Size)
		}
	}
	usage.Total = usage.Images + usage.Containers + usage.Volumes + usage.BuildCache

	if usage.Images != 4_559_000_000 || usage.Containers != 127_000 || usage.Volumes != 1_592_000_000 {
		t.Fatalf("decomposition wrong: %+v", usage)
	}
	if usage.Total != usage.Images+usage.Containers+usage.Volumes+usage.BuildCache {
		t.Fatalf("total mismatch: %+v", usage)
	}
}

func TestScanJSONLinesSkipsNoise(t *testing.T) {
	out := "docker: loading CLI\n" +
		`{"Type":"Images","Size":"1GB"}` + "\n" +
		"\n" +
		"not json at all\n" +
		`{"broken": ` + "\n"

	records := scanJSONLines(out, sandboxDiskUsageRecord{})
	if len(records) != 1 || records[0].Size != "1GB" {
		t.Fatalf("records = %+v, want one Images record", records)
	}
}
