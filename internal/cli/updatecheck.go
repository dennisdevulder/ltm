package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// How long a cached check stays fresh before we refresh in the background.
const updateCheckTTL = 24 * time.Hour

// Commands that should never trigger the warning.
var updateCheckSkip = map[string]bool{
	"update":     true,
	"help":       true,
	"completion": true,
	"server":     true,
}

type cachedUpdateCheck struct {
	CheckedAt time.Time `json:"checked_at"`
	LatestTag string    `json:"latest_tag"`
}

// maybeWarnAboutUpdate is registered as the root command's PersistentPostRun.
// It reads a cached "last known latest version" and prints a one-line warning
// if the running binary is older. When the cache is stale or missing, it
// fires off a detached 'ltm update --check' subprocess to refresh the cache
// for the NEXT invocation — never the current one, so no foreground latency.
func maybeWarnAboutUpdate(cmd *cobra.Command) {
	if os.Getenv("LTM_NO_UPDATE_CHECK") != "" {
		return
	}
	// Dev builds are always newer-than-latest by convention; don't spam.
	if strings.HasSuffix(Version, "-dev") {
		return
	}
	if updateCheckSkip[cmd.Name()] {
		return
	}

	current := stripV(Version)

	cache, fresh := readUpdateCheckCache()
	if !fresh {
		spawnBackgroundRefresh()
	}
	if cache.LatestTag == "" {
		return
	}
	latest := stripV(cache.LatestTag)
	if current == latest {
		return
	}
	fmt.Fprintf(os.Stderr, "\n! ltm v%s is available (you have v%s). run: ltm update\n", latest, current)
}

// writeUpdateCheckCache persists a fresh check result. Called from 'ltm update --check'.
func writeUpdateCheckCache(tag string) {
	path, err := updateCheckCachePath()
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	b, _ := json.Marshal(cachedUpdateCheck{CheckedAt: time.Now().UTC(), LatestTag: tag})
	_ = os.WriteFile(path, b, 0o600)
}

// readUpdateCheckCache returns the cache entry and whether it's still fresh.
func readUpdateCheckCache() (cachedUpdateCheck, bool) {
	var empty cachedUpdateCheck
	path, err := updateCheckCachePath()
	if err != nil {
		return empty, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return empty, false
	}
	var c cachedUpdateCheck
	if err := json.Unmarshal(b, &c); err != nil {
		return empty, false
	}
	return c, time.Since(c.CheckedAt) < updateCheckTTL
}

func updateCheckCachePath() (string, error) {
	var base string
	if v := os.Getenv("LTM_CACHE_DIR"); v != "" {
		base = v
	} else if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		base = filepath.Join(v, "ltm")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache", "ltm")
	}
	return filepath.Join(base, "update-check.json"), nil
}

// spawnBackgroundRefresh launches 'ltm update --check' detached from this
// process. stdout/stderr go to /dev/null so nothing leaks into the user's
// terminal. The subprocess writes the cache; next invocation reads it.
func spawnBackgroundRefresh() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	c := exec.Command(exe, "update", "--check")
	c.Stdin = devnull
	c.Stdout = devnull
	c.Stderr = devnull
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Don't inherit LTM_* env vars that might skew the check.
	c.Env = append(os.Environ(), "LTM_NO_UPDATE_CHECK=1")
	_ = c.Start()
	// explicitly do not Wait; let it run detached
	go func() { _ = c.Wait(); devnull.Close() }()
}
