package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ensureSourceTag(cfg config, tag, commit string) error {
	if existing, err := output(cfg.arise, "git", "rev-parse", "--verify", "refs/tags/"+tag+"^{commit}"); err == nil {
		if existing != commit {
			return fmt.Errorf("tag %s already names a different source commit", tag)
		}
		return nil
	}
	return run(cfg.arise, nil, "git", "tag", "-a", tag, commit, "-m", "arise "+tag)
}

type releaseInfo struct {
	TagName string `json:"tagName"`
	IsDraft bool   `json:"isDraft"`
	Assets  []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

func readRelease(dir, repo, tag string) (releaseInfo, bool, error) {
	cmd := exec.Command("gh", "release", "view", tag, "--repo", repo, "--json", "tagName,assets,isDraft")
	cmd.Dir = dir
	data, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(data), "release not found") || strings.Contains(string(data), "HTTP 404") {
			return releaseInfo{}, false, nil
		}
		return releaseInfo{}, false, fmt.Errorf("inspect release %s/%s: %w: %s", repo, tag, err, data)
	}
	var info releaseInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return info, false, err
	}
	if info.TagName != tag {
		return info, false, fmt.Errorf("release identity mismatch for %s", tag)
	}
	return info, true, nil
}

func ensureRelease(dir, repo, tag string, create []string) error {
	_, exists, err := readRelease(dir, repo, tag)
	if err != nil || exists {
		return err
	}
	return run(dir, nil, "gh", create...)
}

func ensurePublicRelease(dir, repo, tag string) error {
	info, exists, err := readRelease(dir, repo, tag)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("release %s/%s is unavailable", repo, tag)
	}
	if info.IsDraft {
		return run(dir, nil, "gh", "release", "edit", tag, "--repo", repo, "--draft=false")
	}
	return nil
}

func ensureReleaseAsset(dir, repo, tag, artifact, digest string) error {
	info, exists, err := readRelease(dir, repo, tag)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("release %s/%s is unavailable", repo, tag)
	}
	name := filepath.Base(artifact)
	for _, asset := range info.Assets {
		if asset.Name != name {
			continue
		}
		work, err := os.MkdirTemp("", "arise-release-asset-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(work)
		if err := run(dir, nil, "gh", "release", "download", tag, "--repo", repo, "--pattern", name, "--dir", work); err != nil {
			return err
		}
		actual, err := hashFile(filepath.Join(work, name))
		if err != nil {
			return err
		}
		if actual != digest {
			return fmt.Errorf("published asset %s has different content; refusing replacement", name)
		}
		return nil
	}
	return run(dir, nil, "gh", "release", "upload", tag, artifact, "--repo", repo)
}
