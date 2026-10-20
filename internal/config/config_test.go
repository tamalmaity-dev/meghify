package config

import "testing"

// Test binaries run without the release ldflags, so version.Version keeps its
// "dev" default and the helper falls back to the floating :latest tag.
func TestEffectiveHelperImageFallsBackToLatest(t *testing.T) {
	cfg := &Config{}
	if got := cfg.EffectiveHelperImage(); got != "ghcr.io/tamalmaity-dev/meghify-helper:latest" {
		t.Fatalf("EffectiveHelperImage() = %q, want the :latest fallback", got)
	}
}

// A pinned BUILD_HELPER_IMAGE wins over the release default. Surrounding
// whitespace from an env file is trimmed before the reference is used.
func TestEffectiveHelperImagePrefersOverride(t *testing.T) {
	cfg := &Config{BuildHelperImage: "  ghcr.io/tamalmaity-dev/meghify-helper:test  "}
	if got := cfg.EffectiveHelperImage(); got != "ghcr.io/tamalmaity-dev/meghify-helper:test" {
		t.Fatalf("EffectiveHelperImage() = %q, want the trimmed override", got)
	}
}
