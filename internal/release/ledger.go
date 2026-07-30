package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const Schema = 1

type Ledger struct {
	Schema           int    `json:"schema"`
	Version          string `json:"version"`
	SourceCommit     string `json:"source_commit"`
	OverlayBase      string `json:"overlay_base"`
	Artifact         string `json:"artifact"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	Prepared         bool   `json:"prepared"`
	Verified         bool   `json:"verified"`
	SourcePublished  bool   `json:"source_published"`
	AssetPublished   bool   `json:"asset_published"`
	OverlayCommit    string `json:"overlay_commit,omitempty"`
	OverlayPublished bool   `json:"overlay_published"`
}

func Load(path string) (Ledger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Ledger{}, err
	}
	var ledger Ledger
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ledger); err != nil {
		return Ledger{}, err
	}
	if ledger.Schema != Schema || ledger.Version == "" {
		return Ledger{}, errors.New("unsupported or incomplete release ledger")
	}
	return ledger, nil
}

func Save(path string, ledger Ledger) error {
	ledger.Schema = Schema
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ledger-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func ValidateIdentity(ledger Ledger, version, sourceCommit, overlayBase, artifactSHA string) error {
	if ledger.Version != version {
		return fmt.Errorf("ledger version %s does not match %s", ledger.Version, version)
	}
	if ledger.SourceCommit != sourceCommit || ledger.OverlayBase != overlayBase {
		return errors.New("repository commits changed after release preparation")
	}
	if artifactSHA != "" && ledger.ArtifactSHA256 != artifactSHA {
		return errors.New("release artifact changed after preparation")
	}
	return nil
}
