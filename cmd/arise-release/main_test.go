package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	rel "github.com/airencracken/arise-release/internal/release"
)

func TestRenderOverlayIsAtomicAndPinned(t *testing.T) {
	overlay := t.TempDir()
	if err := os.MkdirAll(filepath.Join(overlay, "sys-apps", "arise"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "Makefile"), []byte("VERSION ?= 0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config{version: "0.0.2", overlay: overlay}
	if err := renderOverlay(cfg, rel.Ledger{SourceCommit: strings.Repeat("a", 40)}); err != nil {
		t.Fatal(err)
	}
	ebuild, err := os.ReadFile(filepath.Join(overlay, "sys-apps", "arise", "arise-0.0.2.ebuild"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ebuild), strings.Repeat("a", 40)) {
		t.Fatalf("ebuild = %q", ebuild)
	}
	if !strings.Contains(string(ebuild), "inherit shell-completion go-module") {
		t.Fatalf("embedded release template was not rendered: %q", ebuild)
	}
	makefile, err := os.ReadFile(filepath.Join(overlay, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(makefile) != "VERSION ?= 0.0.2\n" {
		t.Fatalf("Makefile = %q", makefile)
	}
	if err := renderOverlay(cfg, rel.Ledger{SourceCommit: "different"}); err == nil {
		t.Fatal("existing versioned ebuild was overwritten")
	}
}

func TestHashFileDetectsContentMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("content mutation retained artifact digest")
	}
}

func TestAssetReleaseTargetsMaster(t *testing.T) {
	args := assetReleaseArgs("v0.0.12", "/tmp/vendor.tar.xz", "0.0.12", strings.Repeat("a", 40), "digest")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--repo airencracken/arise-overlay-assets --target master") {
		t.Fatalf("asset release arguments do not target master: %q", joined)
	}
	if strings.Contains(joined, "--target main") {
		t.Fatalf("asset release arguments retain obsolete main branch: %q", joined)
	}
}

func TestOverlayPublicationPushesWorktreeHeadToMaster(t *testing.T) {
	if got, want := strings.Join(overlayPushArgs(), " "), "push origin HEAD:master"; got != want {
		t.Fatalf("overlay push arguments = %q, want %q", got, want)
	}
}
