package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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
	binaryEbuild, err := os.ReadFile(filepath.Join(overlay, "sys-apps", "arise-bin", "arise-bin-0.0.2.ebuild"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"RESTRICT=\"strip\"", "QA_PREBUILT=\"usr/bin/arise\"", "!sys-apps/arise", "statically linked"} {
		if !strings.Contains(string(binaryEbuild), required) {
			t.Errorf("embedded binary ebuild is missing %q", required)
		}
	}
	if _, err := os.Stat(filepath.Join(overlay, "sys-apps", "arise-bin", "metadata.xml")); err != nil {
		t.Fatalf("binary metadata was not rendered: %v", err)
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

func TestRenderOverlayPreflightsBinaryCollisionWithoutPartialSourceWrite(t *testing.T) {
	overlay := t.TempDir()
	sourceDirectory := filepath.Join(overlay, "sys-apps", "arise")
	binaryDirectory := filepath.Join(overlay, "sys-apps", "arise-bin")
	for _, directory := range []string{sourceDirectory, binaryDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(overlay, "Makefile"), []byte("VERSION ?= 0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binaryTarget := filepath.Join(binaryDirectory, "arise-bin-0.0.2.ebuild")
	if err := os.WriteFile(binaryTarget, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config{version: "0.0.2", overlay: overlay}
	if err := renderOverlay(cfg, rel.Ledger{SourceCommit: strings.Repeat("a", 40)}); err == nil || !strings.Contains(err.Error(), "binary target already exists") {
		t.Fatalf("binary collision error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourceDirectory, "arise-0.0.2.ebuild")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("binary collision left a partial source ebuild: %v", err)
	}
	makefile, err := os.ReadFile(filepath.Join(overlay, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(makefile) != "VERSION ?= 0.0.1\n" {
		t.Fatalf("binary collision changed Makefile: %q", makefile)
	}
}

func TestRenderOverlayAcceptsCanonicalStagedBinaryEbuild(t *testing.T) {
	overlay := t.TempDir()
	sourceDirectory := filepath.Join(overlay, "sys-apps", "arise")
	binaryDirectory := filepath.Join(overlay, "sys-apps", "arise-bin")
	for _, directory := range []string{sourceDirectory, binaryDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(overlay, "Makefile"), []byte("VERSION ?= 0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binaryTarget := filepath.Join(binaryDirectory, "arise-bin-0.0.2.ebuild")
	if err := os.WriteFile(binaryTarget, []byte(overlayBinaryEbuildTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config{version: "0.0.2", overlay: overlay}
	if err := renderOverlay(cfg, rel.Ledger{SourceCommit: strings.Repeat("a", 40)}); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(binaryTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != overlayBinaryEbuildTemplate {
		t.Fatal("canonical staged binary ebuild was changed")
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

func TestBinaryReleaseUploadsImmutableAssetToSourceRelease(t *testing.T) {
	args := strings.Join(binaryReleaseUploadArgs("v0.0.23", "/tmp/arise-bin-0.0.23-linux-amd64.tar.xz"), " ")
	if want := "release upload v0.0.23 /tmp/arise-bin-0.0.23-linux-amd64.tar.xz --repo airencracken/arise"; args != want {
		t.Fatalf("binary upload arguments = %q, want %q", args, want)
	}
	if strings.Contains(args, "--clobber") {
		t.Fatalf("binary publication permits immutable asset replacement: %q", args)
	}
}

func TestBinaryArtifactManifestRejectsUnknownFields(t *testing.T) {
	manifest := binaryArtifactManifest{
		Schema: 1, Version: "0.0.23", SourceCommit: strings.Repeat("a", 40),
		GOOS: "linux", GOARCH: "amd64", GoToolchain: "go version go1.26 linux/amd64",
		BuildMode: "static-exe", BinarySHA256: strings.Repeat("b", 64), SourceDateEpoch: 1,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded binaryArtifactManifest
	if err := decoder.Decode(&decoded); err != nil || decoded != manifest {
		t.Fatalf("binary manifest round trip = %#v, %v", decoded, err)
	}
	malformed := append(data[:len(data)-1], []byte(`,"hostname":"private"}`)...)
	decoder = json.NewDecoder(bytes.NewReader(malformed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err == nil {
		t.Fatal("binary manifest accepted an unrecognized provenance field")
	}
}

func TestPrepareBinaryArtifactIsReproducibleAndComplete(t *testing.T) {
	repository := t.TempDir()
	files := map[string]string{
		"main.go": `package main
import "fmt"
func main() { fmt.Println("arise 0.0.23") }
`,
		"arise.1":                    ".TH ARISE 1\n",
		"misc/arise-completion.bash": "complete -W sync arise\n",
		"README.md":                  "# Arise\n",
		"LICENSE":                    "test license\n",
	}
	for name, contents := range files {
		path := filepath.Join(repository, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commands := [][]string{
		{"git", "init", "-b", "master"},
		{"git", "config", "user.email", "test@example.invalid"},
		{"git", "config", "user.name", "Test"},
		{"git", "add", "."},
		{"git", "commit", "-m", "fixture"},
		{"go", "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w", "-o", "arise", "main.go"},
	}
	for _, command := range commands {
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Dir = repository
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(command, " "), err, output)
		}
	}
	commit, err := output(repository, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{version: "0.0.23", arise: repository}
	first, firstDigest, err := prepareBinaryArtifact(cfg, commit)
	if err != nil {
		t.Fatal(err)
	}
	firstData, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := prepareBinaryArtifact(cfg, commit)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest || !bytes.Equal(firstData, secondData) {
		t.Fatal("identical source commit produced different binary bundles")
	}
	listing, err := output(repository, "tar", "-tJf", second)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"arise", "arise.1", "arise-completion.bash", "arise-artifact-manifest.json", "README.md", "LICENSE",
	} {
		if !strings.Contains(listing, "/"+required) {
			t.Errorf("binary bundle is missing %s: %s", required, listing)
		}
	}
}

func TestOverlayPublicationPushesWorktreeHeadToMaster(t *testing.T) {
	if got, want := strings.Join(overlayPushArgs(), " "), "push origin HEAD:master"; got != want {
		t.Fatalf("overlay push arguments = %q, want %q", got, want)
	}
}
