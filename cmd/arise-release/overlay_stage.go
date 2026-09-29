package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	rel "github.com/airencracken/arise-release/internal/release"
)

func prepareOverlay(cfg config, ledger *rel.Ledger) error {
	if ledger.OverlayValidated {
		return checkPreparedOverlay(cfg, *ledger)
	}
	work, err := os.MkdirTemp("", "arise-release-stage-"+cfg.version+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	staged := filepath.Join(work, "overlay")
	if err := os.Mkdir(staged, 0755); err != nil {
		return err
	}
	archive := exec.Command("git", "archive", ledger.OverlayBase)
	archive.Dir = cfg.overlay
	data, err := archive.Output()
	if err != nil {
		return fmt.Errorf("archive pinned overlay: %w", err)
	}
	extract := exec.Command("tar", "-xf", "-", "-C", staged)
	extract.Stdin = bytes.NewReader(data)
	if out, err := extract.CombinedOutput(); err != nil {
		return fmt.Errorf("extract pinned overlay: %w: %s", err, out)
	}
	stagedCfg := cfg
	stagedCfg.overlay = staged
	if err := renderOverlay(stagedCfg, *ledger); err != nil {
		return err
	}
	removed, err := pruneOverlay(staged, cfg.version)
	if err != nil {
		return err
	}
	if err := validateOverlay(stagedCfg); err != nil {
		return err
	}
	// Use a private index to create the validated commit without modifying
	// the caller's checkout or staging unrelated files.
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(work, "index"), "GIT_WORK_TREE=" + staged}
	if err := run(cfg.overlay, env, "git", "read-tree", ledger.OverlayBase); err != nil {
		return err
	}
	paths := []string{
		"Makefile", "sys-apps/arise/Manifest", "sys-apps/arise/arise-" + cfg.version + ".ebuild",
		"metadata/md5-cache/sys-apps/arise-" + cfg.version,
		"sys-apps/arise-bin/Manifest", "sys-apps/arise-bin/metadata.xml",
		"sys-apps/arise-bin/arise-bin-" + cfg.version + ".ebuild",
		"metadata/md5-cache/sys-apps/arise-bin-" + cfg.version,
	}
	paths = append(paths, removed...)
	if err := run(cfg.overlay, env, "git", append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	tree, err := outputEnv(cfg.overlay, env, "git", "write-tree")
	if err != nil {
		return err
	}
	ref := "refs/heads/release/arise-" + cfg.version
	commit, existingErr := output(cfg.overlay, "git", "rev-parse", "--verify", ref)
	if existingErr == nil {
		existingTree, err := output(cfg.overlay, "git", "rev-parse", commit+"^{tree}")
		if err != nil || existingTree != tree {
			return fmt.Errorf("release branch %s contains different prepared content", ref)
		}
	} else {
		commit, err = output(cfg.overlay, "git", "commit-tree", tree, "-p", ledger.OverlayBase,
			"-m", "sys-apps/arise: release "+cfg.version)
		if err != nil {
			return err
		}
		if err := run(cfg.overlay, nil, "git", "update-ref", ref, commit, ""); err != nil {
			return err
		}
	}
	ledger.OverlayCommit, ledger.OverlayValidated = commit, true
	if err := checkPreparedOverlay(cfg, *ledger); err != nil {
		return err
	}
	return rel.Save(cfg.state, *ledger)
}

func checkPreparedOverlay(cfg config, ledger rel.Ledger) error {
	if ledger.OverlayCommit == "" {
		return fmt.Errorf("validated overlay commit is missing")
	}
	parent, err := output(cfg.overlay, "git", "rev-parse", ledger.OverlayCommit+"^")
	if err != nil || parent != ledger.OverlayBase {
		return fmt.Errorf("prepared overlay commit does not extend its pinned base")
	}
	path := "sys-apps/arise/arise-" + cfg.version + ".ebuild"
	rendered, err := output(cfg.overlay, "git", "show", ledger.OverlayCommit+":"+path)
	if err != nil || rendered != strings.TrimSpace(strings.ReplaceAll(overlayEbuildTemplate, "@ARISE_COMMIT@", ledger.SourceCommit)) {
		return fmt.Errorf("prepared overlay does not contain the pinned source ebuild")
	}
	return nil
}

func outputEnv(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, data)
	}
	return strings.TrimSpace(string(data)), nil
}
