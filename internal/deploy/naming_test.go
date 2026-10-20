package deploy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestEngineNamesComeFromRecordIDs(t *testing.T) {
	app := &db.Application{ID: "11111111-2222-3333-4444-555555555555", Name: "Shared Display Name"}
	if got := ContainerName(app); got != "app-11111111-2222-3333-4444-555555555555" {
		t.Fatalf("ContainerName = %q", got)
	}
	if got := NetworkName(app); got != "meghify-net-11111111-2222-3333-4444-555555555555" {
		t.Fatalf("NetworkName = %q", got)
	}
	if got := ComposeProjectName(app); got != "meghify-app-11111111-2222-3333-4444-555555555555" {
		t.Fatalf("ComposeProjectName = %q", got)
	}
	// A rename must not change any engine-side identifier.
	renamed := &db.Application{ID: app.ID, Name: "Different"}
	if ContainerName(renamed) != ContainerName(app) || NetworkName(renamed) != NetworkName(app) {
		t.Fatal("renaming changed an engine identifier")
	}
}

func TestVolumesAreAlwaysNamedVolumes(t *testing.T) {
	volume := &db.Volume{
		ID:        "abcdef",
		HostPath:  "/etc/ssh",
		MountPath: "/var/lib/data",
		ReadOnly:  true,
	}
	mounts := toMounts([]*db.Volume{volume})
	if len(mounts) != 1 {
		t.Fatalf("mounts = %d, want 1", len(mounts))
	}
	if mounts[0].Source != "meghify-vol-abcdef" {
		t.Fatalf("source = %q, want the named volume", mounts[0].Source)
	}
	if mounts[0].Target != "/var/lib/data" || !mounts[0].ReadOnly {
		t.Fatalf("mount = %+v", mounts[0])
	}
}

func TestTeeWriterCapsTheDurableLog(t *testing.T) {
	var log bytes.Buffer
	writer := newTeeWriter(&log, nil, "deployment")
	writer.limit = 8
	payload := []byte("0123456789abcdef")
	n, err := writer.Write(payload)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("Write returned %d, want %d", n, len(payload))
	}
	if !writer.truncated {
		t.Fatal("writer did not mark the log truncated")
	}
	if !strings.HasPrefix(log.String(), "01234567") {
		t.Fatalf("log does not start with the capped payload: %q", log.String())
	}
	if !strings.Contains(log.String(), "truncated at 50 MiB") {
		t.Fatalf("log is missing the truncation notice: %q", log.String())
	}
}
