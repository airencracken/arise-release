package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLedgerAtomicRoundTripAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "release.json")
	want := Ledger{Version: "1.2.3", SourceCommit: "source", OverlayBase: "overlay", ArtifactSHA256: "digest", Prepared: true}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateIdentity(got, "1.2.3", "source", "overlay", "digest"); err != nil {
		t.Fatal(err)
	}
	if got.Schema != Schema || !got.Prepared {
		t.Fatalf("ledger = %#v", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("atomic save left files: %v", entries)
	}
}

func TestLedgerRejectsMutationAndUnknownSchema(t *testing.T) {
	ledger := Ledger{Version: "1.2.3", SourceCommit: "a", OverlayBase: "b", ArtifactSHA256: "c"}
	for name, args := range map[string][]string{
		"version":  {"1.2.4", "a", "b", "c"},
		"source":   {"1.2.3", "x", "b", "c"},
		"overlay":  {"1.2.3", "a", "x", "c"},
		"artifact": {"1.2.3", "a", "b", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateIdentity(ledger, args[0], args[1], args[2], args[3]); err == nil {
				t.Fatal("mutation accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"schema":99,"version":"1.2.3","extra":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("invalid schema error = %v", err)
	}
}
