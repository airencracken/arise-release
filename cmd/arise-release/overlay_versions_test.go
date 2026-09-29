package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOverlayRetentionKeepsNewPreviousAndLive(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"arise", "arise-bin"} {
		for _, version := range []string{"0.0.9", "0.0.10", "0.0.11", "9999"} {
			for _, relative := range []string{filepath.Join("sys-apps", name, name+"-"+version+".ebuild"), filepath.Join("metadata", "md5-cache", "sys-apps", name+"-"+version)} {
				path := filepath.Join(root, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	removed, err := pruneOverlay(root, "0.0.11")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 4 {
		t.Fatalf("retired paths: %v", removed)
	}
	for _, name := range []string{"arise", "arise-bin"} {
		for _, version := range []string{"0.0.10", "0.0.11", "9999"} {
			if _, err := os.Stat(filepath.Join(root, "sys-apps", name, name+"-"+version+".ebuild")); err != nil {
				t.Fatalf("retained %s-%s: %v", name, version, err)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "sys-apps", name, name+"-0.0.9.ebuild")); !os.IsNotExist(err) {
			t.Fatalf("superseded version remains: %v", err)
		}
	}
	if _, err := pruneOverlay(root, "0.0.8"); err == nil {
		t.Fatal("release rollback accepted")
	}
}
