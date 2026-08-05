package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	rel "github.com/airencracken/arise-release/internal/release"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

//go:embed arise-vendor.ebuild.in
var overlayEbuildTemplate string

//go:embed arise-bin.ebuild.in
var overlayBinaryEbuildTemplate string

//go:embed arise-bin.metadata.xml
var overlayBinaryMetadata string

type config struct {
	version, arise, overlay, state string
}

type binaryArtifactManifest struct {
	Schema          int    `json:"schema"`
	Version         string `json:"version"`
	SourceCommit    string `json:"source_commit"`
	GOOS            string `json:"goos"`
	GOARCH          string `json:"goarch"`
	GoToolchain     string `json:"go_toolchain"`
	BuildMode       string `json:"build_mode"`
	BinarySHA256    string `json:"binary_sha256"`
	SourceDateEpoch int64  `json:"source_date_epoch"`
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
	if err := run(cfg.arise, nil, "make", "static"); err != nil {
		return err
	}
	artifact := filepath.Join(cfg.arise, "dist", "arise-"+cfg.version+"-vendor.tar.xz")
	digest, err := hashFile(artifact)
	if err != nil {
		return err
	}
	binaryArtifact, binaryDigest, err := prepareBinaryArtifact(cfg, source)
	if err != nil {
		return err
	}
	return rel.Save(cfg.state, rel.Ledger{
		Version: cfg.version, SourceCommit: source, OverlayBase: overlay,
		Artifact: artifact, ArtifactSHA256: digest,
		BinaryArtifact: binaryArtifact, BinarySHA256: binaryDigest, Prepared: true,
	})
}

func prepareBinaryArtifact(cfg config, sourceCommit string) (string, string, error) {
	binary := filepath.Join(cfg.arise, "arise")
	reported, err := output(cfg.arise, binary, "--version")
	if err != nil {
		return "", "", err
	}
	if reported != "arise "+cfg.version {
		return "", "", fmt.Errorf("release binary reports %q, expected %q", reported, "arise "+cfg.version)
	}
	linkage, err := output(cfg.arise, "file", binary)
	if err != nil {
		return "", "", err
	}
	if !strings.Contains(linkage, "ELF 64-bit LSB executable") || !strings.Contains(linkage, "x86-64") || !strings.Contains(linkage, "statically linked") {
		return "", "", fmt.Errorf("release binary is not a static linux-amd64 ELF: %s", linkage)
	}
	epochText, err := output(cfg.arise, "git", "show", "-s", "--format=%ct", sourceCommit)
	if err != nil {
		return "", "", err
	}
	epoch, err := strconv.ParseInt(epochText, 10, 64)
	if err != nil || epoch <= 0 {
		return "", "", fmt.Errorf("invalid source commit epoch %q", epochText)
	}
	toolchain, err := output(cfg.arise, "go", "version")
	if err != nil {
		return "", "", err
	}
	binaryDigest, err := hashFile(binary)
	if err != nil {
		return "", "", err
	}
	dist := filepath.Join(cfg.arise, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return "", "", err
	}
	work, err := os.MkdirTemp(dist, ".arise-bin-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(work)
	rootName := "arise-bin-" + cfg.version + "-linux-amd64"
	root := filepath.Join(work, rootName)
	if err := os.Mkdir(root, 0o755); err != nil {
		return "", "", err
	}
	files := []struct {
		source, target string
		mode           os.FileMode
	}{
		{binary, "arise", 0o755},
		{filepath.Join(cfg.arise, "arise.1"), "arise.1", 0o644},
		{filepath.Join(cfg.arise, "misc", "arise-completion.bash"), "arise-completion.bash", 0o644},
		{filepath.Join(cfg.arise, "README.md"), "README.md", 0o644},
		{filepath.Join(cfg.arise, "LICENSE"), "LICENSE", 0o644},
	}
	for _, file := range files {
		if err := copyFile(file.source, filepath.Join(root, file.target), file.mode); err != nil {
			return "", "", err
		}
	}
	manifest := binaryArtifactManifest{
		Schema: 1, Version: cfg.version, SourceCommit: sourceCommit,
		GOOS: "linux", GOARCH: "amd64", GoToolchain: toolchain,
		BuildMode: "static-exe", BinarySHA256: binaryDigest, SourceDateEpoch: epoch,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", "", err
	}
	manifestData = append(manifestData, '\n')
	if err := atomicWrite(filepath.Join(root, "arise-artifact-manifest.json"), manifestData, 0o644); err != nil {
		return "", "", err
	}
	artifact := filepath.Join(dist, rootName+".tar.xz")
	if err := run(work, []string{"XZ_OPT=-9e"}, "tar",
		"--sort=name", "--mtime=@"+epochText, "--owner=0", "--group=0", "--numeric-owner",
		"-cJf", artifact, rootName); err != nil {
		return "", "", err
	}
	artifactDigest, err := hashFile(artifact)
	if err != nil {
		return "", "", err
	}
	return artifact, artifactDigest, nil
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
		if err := run(cfg.arise, nil, "git", sourcePushArgs(tag)...); err != nil {
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
	if !ledger.BinaryPublished {
		if err := run(cfg.arise, nil, "gh", binaryReleaseUploadArgs(tag, ledger.BinaryArtifact)...); err != nil {
			return err
		}
		ledger.BinaryPublished = true
		if err := rel.Save(cfg.state, ledger); err != nil {
			return err
		}
	}
	if !ledger.AssetPublished {
		if err := run(cfg.arise, nil, "gh", assetReleaseArgs(tag, ledger.Artifact, cfg.version, ledger.SourceCommit, ledger.ArtifactSHA256)...); err != nil {
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
			"sys-apps/arise/arise-"+cfg.version+".ebuild", "metadata/md5-cache/sys-apps/arise-"+cfg.version,
			"sys-apps/arise-bin/Manifest", "sys-apps/arise-bin/metadata.xml",
			"sys-apps/arise-bin/arise-bin-"+cfg.version+".ebuild", "metadata/md5-cache/sys-apps/arise-bin-"+cfg.version); err != nil {
			return err
		}
		if err := run(cfg.overlay, nil, "git", "commit", "-m", "sys-apps/arise: release "+cfg.version); err != nil {
			return err
		}
		commit, err := output(cfg.overlay, "git", "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if err := run(cfg.overlay, nil, "git", overlayPushArgs()...); err != nil {
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

func assetReleaseArgs(tag, artifact, version, sourceCommit, digest string) []string {
	notes := fmt.Sprintf("Immutable offline vendor sources for Arise %s. Source commit: %s. SHA-256: %s.", version, sourceCommit, digest)
	return []string{
		"release", "create", tag, artifact,
		"--repo", "airencracken/arise-overlay-assets", "--target", "master",
		"--title", "Arise " + version + " vendor sources", "--notes", notes,
	}
}

func binaryReleaseUploadArgs(tag, artifact string) []string {
	return []string{"release", "upload", tag, artifact, "--repo", "airencracken/arise"}
}

func overlayPushArgs() []string {
	return []string{"push", "origin", "HEAD:master"}
}

func sourcePushArgs(tag string) []string {
	return []string{"push", "origin", "HEAD:master", tag}
}

func renderOverlay(cfg config, ledger rel.Ledger) error {
	rendered := strings.ReplaceAll(overlayEbuildTemplate, "@ARISE_COMMIT@", ledger.SourceCommit)
	target := filepath.Join(cfg.overlay, "sys-apps", "arise", "arise-"+cfg.version+".ebuild")
	binaryTarget := filepath.Join(cfg.overlay, "sys-apps", "arise-bin", "arise-bin-"+cfg.version+".ebuild")
	binaryMetadata := filepath.Join(filepath.Dir(binaryTarget), "metadata.xml")
	if _, err := os.Stat(target); err == nil {
		return errors.New("overlay target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	binaryCreated := false
	if existing, err := os.ReadFile(binaryTarget); err == nil {
		if string(existing) != overlayBinaryEbuildTemplate {
			return errors.New("binary target already exists with non-canonical content")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		binaryCreated = true
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
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(binaryTarget), 0o755); err != nil {
		return err
	}
	metadataCreated := false
	if _, err := os.Stat(binaryMetadata); errors.Is(err, os.ErrNotExist) {
		if err := atomicWrite(binaryMetadata, []byte(overlayBinaryMetadata), 0o644); err != nil {
			return err
		}
		metadataCreated = true
	} else if err != nil {
		return err
	}
	rollback := func() {
		_ = os.Remove(target)
		if binaryCreated {
			_ = os.Remove(binaryTarget)
		}
		if metadataCreated {
			_ = os.Remove(binaryMetadata)
		}
	}
	if err := atomicWrite(target, []byte(rendered), 0o644); err != nil {
		rollback()
		return err
	}
	if binaryCreated {
		if err := atomicWrite(binaryTarget, []byte(overlayBinaryEbuildTemplate), 0o644); err != nil {
			rollback()
			return err
		}
	}
	if err := atomicWrite(makefilePath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		rollback()
		return err
	}
	return nil
}

func validateOverlay(cfg config) error {
	target := filepath.Join(cfg.overlay, "sys-apps", "arise", "arise-"+cfg.version+".ebuild")
	binaryTarget := filepath.Join(cfg.overlay, "sys-apps", "arise-bin", "arise-bin-"+cfg.version+".ebuild")
	dist := "/tmp/arise-overlay-distfiles-" + cfg.version
	portage := "/tmp/arise-overlay-portage-" + cfg.version
	env := []string{"DISTDIR=" + dist}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(cfg.arise, "dist", "arise-bin-"+cfg.version+"-linux-amd64.tar.xz"),
		filepath.Join(dist, "arise-bin-"+cfg.version+"-linux-amd64.tar.xz"), 0o644); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := run(cfg.overlay, env, "ebuild", "--force", target, "manifest"); err != nil {
		return err
	}
	if err := run(cfg.overlay, env, "ebuild", "--force", binaryTarget, "manifest"); err != nil {
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
	if err := run(cfg.overlay, env, "ebuild", target, "clean", "unpack", "compile", "test"); err != nil {
		return err
	}
	return run(cfg.overlay, env, "ebuild", binaryTarget, "clean", "unpack", "prepare")
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
	binaryDigest := ""
	if withArtifact {
		digest, err = hashFile(ledger.Artifact)
		if err != nil {
			return ledger, err
		}
		binaryDigest, err = hashFile(ledger.BinaryArtifact)
		if err != nil {
			return ledger, err
		}
	}
	return ledger, rel.ValidateIdentity(ledger, cfg.version, source, overlay, digest, binaryDigest)
}

func requireClean(dir string) error {
	status, err := output(dir, "git", "status", "--porcelain", "--untracked-files=no")
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

func copyFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
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
