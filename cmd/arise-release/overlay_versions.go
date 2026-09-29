package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func versionLess(a, b string) bool {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for index := 0; index < 3; index++ {
		// Release versions have already passed versionPattern. Compare decimal
		// lengths to avoid overflow for arbitrarily large version components.
		x, y := strings.TrimLeft(left[index], "0"), strings.TrimLeft(right[index], "0")
		if len(x) != len(y) {
			return len(x) < len(y)
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// Keep the new version and one previous version of each package. These are
// private staging files; the returned paths also stage their tracked removal.
func pruneOverlay(root, version string) ([]string, error) {
	var removed []string
	for _, name := range []string{"arise", "arise-bin"} {
		directory := filepath.Join(root, "sys-apps", name)
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		versions := make(map[string]string)
		previous := ""
		for _, entry := range entries {
			base := entry.Name()
			if !strings.HasPrefix(base, name+"-") || !strings.HasSuffix(base, ".ebuild") {
				continue
			}
			candidate := strings.TrimSuffix(strings.TrimPrefix(base, name+"-"), ".ebuild")
			if !versionPattern.MatchString(candidate) || candidate == version {
				continue
			}
			if versionLess(version, candidate) {
				return nil, fmt.Errorf("release %s precedes existing overlay version %s", version, candidate)
			}
			versions[candidate] = base
			if previous == "" || versionLess(previous, candidate) {
				previous = candidate
			}
		}
		for candidate, base := range versions {
			if candidate == previous {
				continue
			}
			for _, relative := range []string{filepath.Join("sys-apps", name, base), filepath.Join("metadata", "md5-cache", "sys-apps", name+"-"+candidate)} {
				if err := os.Remove(filepath.Join(root, relative)); err != nil && !os.IsNotExist(err) {
					return nil, err
				}
				removed = append(removed, relative)
			}
		}
	}
	return removed, nil
}
