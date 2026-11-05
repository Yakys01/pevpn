// Package extstore reads an unpacked extension's chrome.storage.local from
// Brave's LevelDB-backed "Local Extension Settings" directory.
package extstore

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// BraveRoot is the Brave user-data root. Override with UPVPN_BRAVE_ROOT.
func BraveRoot() string {
	if r := os.Getenv("UPVPN_BRAVE_ROOT"); r != "" {
		return r
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "BraveSoftware", "Brave-Browser")
}

// Dir locates the storage directory for extension extID. When profileDir is
// set it is used verbatim; otherwise every profile is scanned and the one with
// the most recently written data wins.
func Dir(profileDir, extID string) (string, error) {
	if profileDir != "" {
		d := filepath.Join(profileDir, "Local Extension Settings", extID)
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d, nil
		}
		return "", os.ErrNotExist
	}
	matches, _ := filepath.Glob(filepath.Join(BraveRoot(), "*", "Local Extension Settings", extID))
	best, bestMod := "", time.Time{}
	for _, d := range matches {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		if mod := newest(d); best == "" || mod.After(bestMod) {
			best, bestMod = d, mod
		}
	}
	if best == "" {
		return "", os.ErrNotExist
	}
	return best, nil
}

// Load returns the concatenated bytes of every data file of extID, oldest
// first, so the last occurrence of a key is the freshest one.
func Load(profileDir, extID string) ([]byte, error) {
	dir, err := Dir(profileDir, extID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || e.Name() == "LOCK" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	var out []byte
	for _, f := range files {
		if b, err := os.ReadFile(f.path); err == nil {
			out = append(out, b...)
		}
	}
	return out, nil
}

func newest(dir string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var t time.Time
	for _, e := range entries {
		if e.IsDir() || e.Name() == "LOCK" {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
	}
	return t
}
