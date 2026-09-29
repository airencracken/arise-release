package main

import (
	rel "github.com/airencracken/arise-release/internal/release"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func auditReleaseFixture(t *testing.T) (config, rel.Ledger) {
	t.Helper()
	base := t.TempDir()
	cfg := config{version: "0.0.99", arise: filepath.Join(base, "source"), overlay: filepath.Join(base, "overlay"), state: filepath.Join(base, "ledger.json")}
	for _, d := range []string{cfg.arise, cfg.overlay} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "Makefile"), []byte("VERSION ?= 0.0.98\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, ".gitignore"), []byte("dist/\n"), 0644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"init", "-b", "master"}, {"config", "user.email", "audit@example.invalid"}, {"config", "user.name", "Audit"}, {"add", "."}, {"commit", "-m", "fixture"}} {
			c := exec.Command("git", args...)
			c.Dir = d
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git: %v: %s", err, out)
			}
		}
	}
	source, err := output(cfg.arise, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := output(cfg.overlay, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.arise, "dist"), 0755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(cfg.arise, "dist", "arise-0.0.99-vendor.tar.xz")
	bin := filepath.Join(cfg.arise, "dist", "arise-bin-0.0.99-linux-amd64.tar.xz")
	for _, p := range []string{a, bin} {
		if err := os.WriteFile(p, []byte("audit artifact"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := hashFile(a)
	if err != nil {
		t.Fatal(err)
	}
	l := rel.Ledger{Version: cfg.version, SourceCommit: source, OverlayBase: overlay, Artifact: a, ArtifactSHA256: h, BinaryArtifact: bin, BinarySHA256: h, Prepared: true, Verified: true}
	if err := rel.Save(cfg.state, l); err != nil {
		t.Fatal(err)
	}
	return cfg, l
}

func TestAuditSourcePublicationRetryAfterPushFailure(t *testing.T) {
	cfg, ledger := auditReleaseFixture(t)
	if err := renderOverlay(cfg, ledger); err != nil {
		t.Fatal(err)
	}
	if err := run(cfg.overlay, nil, "git", "add", "."); err != nil {
		t.Fatal(err)
	}
	if err := run(cfg.overlay, nil, "git", "commit", "-m", "prepared fixture"); err != nil {
		t.Fatal(err)
	}
	commit, err := output(cfg.overlay, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ledger.OverlayCommit, ledger.OverlayValidated = commit, true
	if err := rel.Save(cfg.state, ledger); err != nil {
		t.Fatal(err)
	}
	first := publish(cfg)
	if first == nil {
		t.Fatal("missing origin unexpectedly pushed")
	}
	if _, err := output(cfg.arise, "git", "rev-parse", "refs/tags/v0.0.99"); err != nil {
		t.Fatalf("test did not reach the tag/push boundary: %v; publication: %v", err, first)
	}
	second := publish(cfg)
	if second != nil && strings.Contains(second.Error(), "git tag -a") {
		t.Fatalf("retry is blocked by locally created tag after first push failure: %v", second)
	}
}

func TestAuditOverlayRenderingCanResume(t *testing.T) {
	cfg, l := auditReleaseFixture(t)
	if err := renderOverlay(cfg, l); err != nil {
		t.Fatal(err)
	}
	if err := renderOverlay(cfg, l); err != nil {
		t.Fatalf("retry after validation/interruption fails even with identical pinned inputs: %v", err)
	}
}

func TestAuditReleaseRejectsDirtyPreparedSource(t *testing.T) {
	cfg, _ := auditReleaseFixture(t)
	if err := os.WriteFile(filepath.Join(cfg.arise, "Makefile"), []byte("dirty release build input\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAndCheck(cfg, true); err == nil {
		t.Fatal("prepared identity accepts dirty source: verify tests different working-tree content than tagged release")
	}
}

func TestAuditPublicationWaitsForOverlayValidation(t *testing.T) {
	cfg, _ := auditReleaseFixture(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	c := exec.Command("git", "remote", "add", "origin", origin)
	c.Dir = cfg.arise
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v: %s", err, out)
	}
	fakes := t.TempDir()
	trace := filepath.Join(fakes, "trace")
	t.Setenv("ARISE_RELEASE_AUDIT_TRACE", trace)
	for name, body := range map[string]string{
		"gh":     "#!/bin/bash\nprintf 'publication: %s\\n' \"$*\" >> \"$ARISE_RELEASE_AUDIT_TRACE\"\nexit 0\n",
		"ebuild": "#!/bin/bash\nprintf 'validation failure: %s\\n' \"$*\" >> \"$ARISE_RELEASE_AUDIT_TRACE\"\nexit 42\n",
	} {
		if err := os.WriteFile(filepath.Join(fakes, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := publish(cfg)
	if err == nil {
		t.Fatal("injected validation failure ignored")
	}
	events, readErr := os.ReadFile(trace)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.HasPrefix(string(events), "publication:") {
		t.Fatalf("publications completed before failed overlay gate: %s", events)
	}
}
