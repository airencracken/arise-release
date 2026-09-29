package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	rel "github.com/airencracken/arise-release/internal/release"
)

func stageReleaseFixture(t *testing.T, cfg config, ledger rel.Ledger) rel.Ledger {
	t.Helper()
	if err := renderOverlay(cfg, ledger); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "prepared overlay"}} {
		if err := run(cfg.overlay, nil, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	commit, err := output(cfg.overlay, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ledger.OverlayCommit, ledger.OverlayValidated = commit, true
	if err := rel.Save(cfg.state, ledger); err != nil {
		t.Fatal(err)
	}
	return ledger
}

func TestPublicationReconcilesCompletedRemoteSteps(t *testing.T) {
	cfg, ledger := auditReleaseFixture(t)
	ledger = stageReleaseFixture(t, cfg, ledger)
	for _, repo := range []string{cfg.arise, cfg.overlay} {
		origin := filepath.Join(t.TempDir(), "origin.git")
		if out, err := exec.Command("git", "init", "--bare", origin).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if err := run(repo, nil, "git", "remote", "add", "origin", origin); err != nil {
			t.Fatal(err)
		}
	}
	fixture := t.TempDir()
	t.Setenv("ARISE_RELEASE_GH_FIXTURE", fixture)
	t.Setenv("ARISE_RELEASE_TEST_BINARY", os.Args[0])
	t.Setenv("ARISE_RELEASE_FAIL_CREATE", "airencracken/arise")
	wrapper := "#!/bin/sh\nexec \"$ARISE_RELEASE_TEST_BINARY\" -test.run=^TestReleaseCommandFixture$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(fixture, "gh"), []byte(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fixture+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := publish(cfg); err == nil {
		t.Fatal("injected post-create interruption ignored")
	}
	t.Setenv("ARISE_RELEASE_FAIL_CREATE", "")
	if err := publish(cfg); err != nil {
		t.Fatalf("retry after remote create: %v", err)
	}
	if err := publish(cfg); err != nil {
		t.Fatalf("completed publication retry: %v", err)
	}
	if err := rel.Save(cfg.state, ledger); err != nil {
		t.Fatal(err)
	}
	if err := publish(cfg); err != nil {
		t.Fatalf("reconcile existing releases/assets/overlay push: %v", err)
	}
	events, err := os.ReadFile(filepath.Join(fixture, "events"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(events), "create airencracken/arise\n") != 1 || strings.Count(string(events), "upload airencracken/arise\n") != 1 {
		t.Fatalf("retry duplicated immutable publication: %s", events)
	}
	// Existing assets must be checked, rather than blindly accepting a name.
	published := filepath.Join(fixture, "airencracken_arise", "v0.0.99", filepath.Base(ledger.BinaryArtifact))
	if err := os.WriteFile(published, []byte("different payload"), 0644); err != nil {
		t.Fatal(err)
	}
	ledger.SourcePublished, ledger.AssetPublished, ledger.OverlayPublished = true, true, true
	ledger.BinaryPublished = false
	if err := rel.Save(cfg.state, ledger); err != nil {
		t.Fatal(err)
	}
	if err := publish(cfg); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("conflicting remote asset accepted: %v", err)
	}
}

func TestReleaseCommandFixture(t *testing.T) {
	root := os.Getenv("ARISE_RELEASE_GH_FIXTURE")
	if root == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 4 {
		os.Exit(2)
	}
	args = args[1:]
	option := func(name string) string {
		for i := range args {
			if args[i] == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	repo, tag, op := option("--repo"), args[2], args[1]
	directory := filepath.Join(root, strings.ReplaceAll(repo, "/", "_"), tag)
	if op == "view" {
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			os.Stderr.WriteString("HTTP 404: release not found\n")
			os.Exit(1)
		}
		if err != nil {
			os.Exit(2)
		}
		info := releaseInfo{TagName: tag}
		for _, entry := range entries {
			info.Assets = append(info.Assets, struct {
				Name string `json:"name"`
			}{entry.Name()})
		}
		json.NewEncoder(os.Stdout).Encode(info)
		os.Exit(0)
	}
	events, err := os.OpenFile(filepath.Join(root, "events"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		os.Exit(2)
	}
	io.WriteString(events, op+" "+repo+"\n")
	events.Close()
	copy := func(from, to string) {
		data, err := os.ReadFile(from)
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(to, data, 0644); err != nil {
			os.Exit(2)
		}
	}
	switch op {
	case "create":
		if err := os.MkdirAll(directory, 0755); err != nil {
			os.Exit(2)
		}
		if len(args) > 3 && !strings.HasPrefix(args[3], "--") {
			copy(args[3], filepath.Join(directory, filepath.Base(args[3])))
		}
		if repo == os.Getenv("ARISE_RELEASE_FAIL_CREATE") {
			os.Stderr.WriteString("simulated lost response after creation\n")
			os.Exit(1)
		}
	case "upload":
		copy(args[3], filepath.Join(directory, filepath.Base(args[3])))
	case "download":
		copy(filepath.Join(directory, option("--pattern")), filepath.Join(option("--dir"), option("--pattern")))
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
