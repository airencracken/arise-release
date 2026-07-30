package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	rel "github.com/airencracken/arise-release/internal/release"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type config struct {
	version, arise, overlay, state string
}

func main() {
	if len(os.Args) < 3 {
		fail("usage: arise-release prepare|verify|publish VERSION [options]")
	}
	stage, version := os.Args[1], os.Args[2]
	if !versionPattern.MatchString(version) {
		fail("VERSION must use MAJOR.MINOR.PATCH")
	}
	flags := flag.NewFlagSet(stage, flag.ExitOnError)
	arise := flags.String("arise", "../arise", "Arise source repository")
	overlay := flags.String("overlay", "../arise-overlay", "Arise overlay repository")
	state := flags.String("state", "", "release ledger path")
	if err := flags.Parse(os.Args[3:]); err != nil {
		fail(err.Error())
	}
	if *state == "" {
		*state = filepath.Join(".release", "arise-"+version+".json")
	}
	cfg := config{version: version, arise: absolute(*arise), overlay: absolute(*overlay), state: absolute(*state)}
	var err error
	switch stage {
	case "prepare":
		err = prepare(cfg)
	case "verify":
		err = verify(cfg)
	case "publish":
		err = publish(cfg)
	default:
		err = fmt.Errorf("unknown stage %q", stage)
	}
	if err != nil {
		fail(err.Error())
	}
}

func prepare(cfg config) error {
	if _, err := os.Stat(cfg.state); err == nil {
		return errors.New("release ledger already exists; use verify or publish to resume")
	}
	if err := requireClean(cfg.arise); err != nil {
		return err
	}
	if err := requireClean(cfg.overlay); err != nil {
		return err
	}
	source, err := output(cfg.arise, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	overlay, err := output(cfg.overlay, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	makefile, err := os.ReadFile(filepath.Join(cfg.arise, "Makefile"))
	if err != nil {
		return err
	}
	if !strings.Contains(string(makefile), "PROJECT_VERSION := "+cfg.version) {
		return errors.New("source PROJECT_VERSION does not match requested release")
	}
	if err := run(cfg.arise, nil, "make", "deps", "VERSION="+cfg.version); err != nil {
		return err
	}
	artifact := filepath.Join(cfg.arise, "dist", "arise-"+cfg.version+"-vendor.tar.xz")
	digest, err := hashFile(artifact)
	if err != nil {
		return err
	}
	return rel.Save(cfg.state, rel.Ledger{
		Version: cfg.version, SourceCommit: source, OverlayBase: overlay,
		Artifact: artifact, ArtifactSHA256: digest, Prepared: true,
	})
}

func verify(cfg config) error {
	ledger, err := loadAndCheck(cfg, true)
	if err != nil {
		return err
	}
	for _, args := range [][]string{
		{"make", "static", "test", "vet", "test-race"},
		{"make", "bench-quick"},
		{"make", "test-vendor-artifact", "VERSION=" + cfg.version},
	} {
		if err := run(cfg.arise, nil, args[0], args[1:]...); err != nil {
			return err
		}
	}
	ledger.Verified = true
	return rel.Save(cfg.state, ledger)
}

func publish(cfg config) error {
	ledger, err := loadAndCheck(cfg, true)
	if err != nil {
		return err
	}
	if !ledger.Verified {
		return errors.New("release has not passed verify")
	}
	tag := "v" + cfg.version
	if !ledger.SourcePublished {
		if err := run(cfg.arise, nil, "git", "tag", "-a", tag, "-m", "arise "+tag); err != nil {
			return err
		}
		if err := run(cfg.arise, nil, "git", "push", "origin", "master", tag); err != nil {
			return err
		}
		notes := filepath.Join(cfg.arise, "docs", "releases", cfg.version+".md")
		if err := run(cfg.arise, nil, "gh", "release", "create", tag, "--repo", "airencracken/arise", "--title", "Arise "+cfg.version, "--notes-file", notes); err != nil {
			return err
		}
		ledger.SourcePublished = true
		if err := rel.Save(cfg.state, ledger); err != nil {
			return err
		}
	}
	if !ledger.AssetPublished {
		notes := fmt.Sprintf("Immutable offline vendor sources for Arise %s. Source commit: %s. SHA-256: %s.", cfg.version, ledger.SourceCommit, ledger.ArtifactSHA256)
		if err := run(cfg.arise, nil, "gh", "release", "create", tag, ledger.Artifact,
			"--repo", "airencracken/arise-overlay-assets", "--target", "main",
			"--title", "Arise "+cfg.version+" vendor sources", "--notes", notes); err != nil {
			return err
		}
		ledger.AssetPublished = true
		if err := rel.Save(cfg.state, ledger); err != nil {
			return err
		}
	}
	if !ledger.OverlayPublished {
		if err := renderOverlay(cfg, ledger); err != nil {
			return err
		}
		if err := validateOverlay(cfg); err != nil {
			return err
		}
		if err := run(cfg.overlay, nil, "git", "add", "Makefile", "sys-apps/arise/Manifest",
			"sys-apps/arise/arise-"+cfg.version+".ebuild", "metadata/md5-cache/sys-apps/arise-"+cfg.version); err != nil {
			return err
		}
		if err := run(cfg.overlay, nil, "git", "commit", "-m", "sys-apps/arise: release "+cfg.version); err != nil {
			return err
		}
		commit, err := output(cfg.overlay, "git", "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if err := run(cfg.overlay, nil, "git", "push", "origin", "master"); err != nil {
			return err
		}
		ledger.OverlayCommit, ledger.OverlayPublished = commit, true
		if err := rel.Save(cfg.state, ledger); err != nil {
			return err
		}
	}
	fmt.Printf("Arise %s published; live-system installation remains manual.\n", cfg.version)
	return nil
}

func renderOverlay(cfg config, ledger rel.Ledger) error {
	templatePath := filepath.Join(cfg.overlay, "scripts", "templates", "arise-vendor.ebuild.in")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return err
	}
	rendered := strings.ReplaceAll(string(data), "@ARISE_COMMIT@", ledger.SourceCommit)
	target := filepath.Join(cfg.overlay, "sys-apps", "arise", "arise-"+cfg.version+".ebuild")
	if _, err := os.Stat(target); err == nil {
		return errors.New("overlay target already exists")
	}
	if err := atomicWrite(target, []byte(rendered), 0o644); err != nil {
		return err
	}
	makefilePath := filepath.Join(cfg.overlay, "Makefile")
	makefile, err := os.ReadFile(makefilePath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(makefile), "\n")
	found := false
	for i := range lines {
		if strings.HasPrefix(lines[i], "VERSION ?=") {
			lines[i], found = "VERSION ?= "+cfg.version, true
		}
	}
	if !found {
		return errors.New("overlay Makefile has no VERSION default")
	}
	return atomicWrite(makefilePath, []byte(strings.Join(lines, "\n")), 0o644)
}

func validateOverlay(cfg config) error {
	target := filepath.Join(cfg.overlay, "sys-apps", "arise", "arise-"+cfg.version+".ebuild")
	dist := "/tmp/arise-overlay-distfiles-" + cfg.version
	portage := "/tmp/arise-overlay-portage-" + cfg.version
	env := []string{"DISTDIR=" + dist}
	if err := run(cfg.overlay, env, "ebuild", "--force", target, "manifest"); err != nil {
		return err
	}
	repos := fmt.Sprintf("[gentoo]\nlocation = /var/db/repos/gentoo\nauto-sync = no\n\n[arise-overlay]\nlocation = %s\nmasters = gentoo\nauto-sync = no\n", cfg.overlay)
	if err := run(cfg.overlay, nil, "egencache", "--repositories-configuration="+repos,
		"--repo=arise-overlay", "--cache-dir="+filepath.Join(cfg.overlay, "metadata/md5-cache"), "--update"); err != nil {
		return err
	}
	if err := run(cfg.overlay, nil, "make", "check"); err != nil {
		return err
	}
	env = []string{"DISTDIR=" + dist, "PORTAGE_TMPDIR=" + portage, "PORTAGE_USERNAME=" + currentUser(), "PORTAGE_GRPNAME=" + currentGroup()}
	return run(cfg.overlay, env, "ebuild", target, "clean", "unpack", "compile", "test")
}

func loadAndCheck(cfg config, withArtifact bool) (rel.Ledger, error) {
	ledger, err := rel.Load(cfg.state)
	if err != nil {
		return ledger, err
	}
	source, err := output(cfg.arise, "git", "rev-parse", "HEAD")
	if err != nil {
		return ledger, err
	}
	overlay, err := output(cfg.overlay, "git", "rev-parse", "HEAD")
	if err != nil {
		return ledger, err
	}
	digest := ""
	if withArtifact {
		digest, err = hashFile(ledger.Artifact)
		if err != nil {
			return ledger, err
		}
	}
	return ledger, rel.ValidateIdentity(ledger, cfg.version, source, overlay, digest)
}

func requireClean(dir string) error {
	status, err := output(dir, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("%s has uncommitted changes", dir)
	}
	return nil
}

func run(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(append([]string{name}, args...), " "), err)
	}
	return nil
}

func output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func absolute(path string) string {
	value, err := filepath.Abs(path)
	if err != nil {
		fail(err.Error())
	}
	return value
}
func currentUser() string  { value, _ := output("", "id", "-un"); return value }
func currentGroup() string { value, _ := output("", "id", "-gn"); return value }
func fail(message string)  { fmt.Fprintln(os.Stderr, "arise-release:", message); os.Exit(1) }
