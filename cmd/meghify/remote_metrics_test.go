package main

import "testing"

func TestParseProcCPU(t *testing.T) {
	idle, total, ok := parseProcCPU("cpu  10 20 30 40 50 60 70 80 90 100")
	if !ok {
		t.Fatal("expected cpu line to parse")
	}
	// Every field contributes to total; fields 3 and 4 are idle + iowait.
	if total != 550 {
		t.Fatalf("total = %d, want 550", total)
	}
	if idle != 90 {
		t.Fatalf("idle = %d, want 90", idle)
	}
	if _, _, ok := parseProcCPU("intr 1 2 3"); ok {
		t.Fatal("non-cpu line must not parse")
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine([]string{"  value\r", "ignored"}); got != "value" {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine(nil); got != "" {
		t.Fatalf("empty firstLine = %q", got)
	}
}
