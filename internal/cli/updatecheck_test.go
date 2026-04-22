package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// ---- updateCheckCachePath ----

func TestUpdateCheckCachePath_LTMCacheDir(t *testing.T) {
	// LTM_CACHE_DIR takes top precedence over XDG_CACHE_HOME and ~/.cache.
	t.Setenv("LTM_CACHE_DIR", "/tmp/ltm-cache-test")
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-should-be-ignored")
	got, err := updateCheckCachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/ltm-cache-test", "update-check.json")
	if got != want {
		t.Errorf("updateCheckCachePath = %q, want %q", got, want)
	}
}

func TestUpdateCheckCachePath_XDGFallback(t *testing.T) {
	// LTM_CACHE_DIR unset → XDG_CACHE_HOME appends /ltm.
	t.Setenv("LTM_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
	got, err := updateCheckCachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/xdg-cache", "ltm", "update-check.json")
	if got != want {
		t.Errorf("updateCheckCachePath = %q, want %q", got, want)
	}
}

func TestUpdateCheckCachePath_HomeFallback(t *testing.T) {
	// Neither env set → ~/.cache/ltm.
	t.Setenv("LTM_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", "")
	got, err := updateCheckCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join(".cache", "ltm", "update-check.json")) {
		t.Errorf("updateCheckCachePath = %q, want ~/.cache/ltm/update-check.json", got)
	}
}

// ---- write / read roundtrip ----

func TestUpdateCheckCache_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LTM_CACHE_DIR", dir)

	writeUpdateCheckCache("v9.9.9")

	// The file should exist with 0600 perms and valid JSON.
	path := filepath.Join(dir, "update-check.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("cache file mode = %o, want 600", mode)
	}

	got, fresh := readUpdateCheckCache()
	if !fresh {
		t.Error("just-written cache should be fresh")
	}
	if got.LatestTag != "v9.9.9" {
		t.Errorf("LatestTag = %q, want v9.9.9", got.LatestTag)
	}
	if time.Since(got.CheckedAt) > time.Minute {
		t.Errorf("CheckedAt = %v, want ~now", got.CheckedAt)
	}
}

func TestReadUpdateCheckCache_MissingFile(t *testing.T) {
	t.Setenv("LTM_CACHE_DIR", t.TempDir())
	got, fresh := readUpdateCheckCache()
	if fresh {
		t.Error("missing file should not be fresh")
	}
	if got.LatestTag != "" {
		t.Errorf("missing file should return zero cache, got %+v", got)
	}
}

func TestReadUpdateCheckCache_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LTM_CACHE_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "update-check.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, fresh := readUpdateCheckCache()
	if fresh {
		t.Error("corrupt JSON should not count as fresh")
	}
	if got.LatestTag != "" {
		t.Errorf("corrupt JSON should return zero cache, got %+v", got)
	}
}

func TestReadUpdateCheckCache_StaleNotFresh(t *testing.T) {
	// Manually write a cache dated well past the TTL — it should round-trip
	// but fresh=false so callers know to refresh.
	dir := t.TempDir()
	t.Setenv("LTM_CACHE_DIR", dir)
	stale := cachedUpdateCheck{
		CheckedAt: time.Now().Add(-48 * time.Hour),
		LatestTag: "v0.9.0",
	}
	b, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(dir, "update-check.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, fresh := readUpdateCheckCache()
	if fresh {
		t.Error("48h-old cache should not be fresh")
	}
	if got.LatestTag != "v0.9.0" {
		t.Errorf("stale cache contents lost: %+v", got)
	}
}

// ---- maybeWarnAboutUpdate ----

// captureStderr redirects os.Stderr for the duration of fn and returns what
// was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		done <- string(buf[:n])
	}()

	fn()
	w.Close()
	os.Stderr = orig
	select {
	case s := <-done:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("captureStderr timeout")
		return ""
	}
}

func TestMaybeWarnAboutUpdate_DevBuildSkips(t *testing.T) {
	// Dev builds end with '-dev' and must never print the warning.
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.1.0-dev"

	t.Setenv("LTM_CACHE_DIR", t.TempDir())
	// seed a cache with a fake newer version to prove the skip is what
	// blocks the warning, not the absence of a cache.
	writeUpdateCheckCache("v99.0.0")

	out := captureStderr(t, func() {
		maybeWarnAboutUpdate(&cobra.Command{Use: "ls"})
	})
	if out != "" {
		t.Errorf("dev build should skip warning, got stderr: %q", out)
	}
}

func TestMaybeWarnAboutUpdate_EnvDisables(t *testing.T) {
	// LTM_NO_UPDATE_CHECK is the escape hatch for scripts and CI —
	// any non-empty value suppresses the warning.
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.0.1"

	t.Setenv("LTM_CACHE_DIR", t.TempDir())
	t.Setenv("LTM_NO_UPDATE_CHECK", "1")
	writeUpdateCheckCache("v99.0.0")

	out := captureStderr(t, func() {
		maybeWarnAboutUpdate(&cobra.Command{Use: "ls"})
	})
	if out != "" {
		t.Errorf("LTM_NO_UPDATE_CHECK should suppress, got stderr: %q", out)
	}
}

func TestMaybeWarnAboutUpdate_SkipsSelectCommands(t *testing.T) {
	// 'update', 'help', 'completion', and 'server' never emit the warning —
	// they either already own the update flow or run headless.
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.0.1"

	t.Setenv("LTM_CACHE_DIR", t.TempDir())
	t.Setenv("LTM_NO_UPDATE_CHECK", "")
	writeUpdateCheckCache("v99.0.0")

	for _, name := range []string{"update", "help", "completion", "server"} {
		t.Run(name, func(t *testing.T) {
			out := captureStderr(t, func() {
				maybeWarnAboutUpdate(&cobra.Command{Use: name})
			})
			if out != "" {
				t.Errorf("command %q should skip warning, got: %q", name, out)
			}
		})
	}
}

func TestMaybeWarnAboutUpdate_PrintsWhenBehind(t *testing.T) {
	// Behind the cached latest and on a regular command → warning should fire.
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.0.1"

	t.Setenv("LTM_CACHE_DIR", t.TempDir())
	t.Setenv("LTM_NO_UPDATE_CHECK", "")
	writeUpdateCheckCache("v0.9.9")

	out := captureStderr(t, func() {
		maybeWarnAboutUpdate(&cobra.Command{Use: "ls"})
	})
	if !strings.Contains(out, "v0.9.9") {
		t.Errorf("expected warning to mention new version, got: %q", out)
	}
	if !strings.Contains(out, "v0.0.1") {
		t.Errorf("expected warning to mention current version, got: %q", out)
	}
	if !strings.Contains(out, "ltm update") {
		t.Errorf("expected warning to suggest 'ltm update', got: %q", out)
	}
}

func TestMaybeWarnAboutUpdate_SilentWhenAtLatest(t *testing.T) {
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.9.9"

	t.Setenv("LTM_CACHE_DIR", t.TempDir())
	t.Setenv("LTM_NO_UPDATE_CHECK", "")
	writeUpdateCheckCache("v0.9.9")

	out := captureStderr(t, func() {
		maybeWarnAboutUpdate(&cobra.Command{Use: "ls"})
	})
	if out != "" {
		t.Errorf("at-latest should not warn, got: %q", out)
	}
}

func TestMaybeWarnAboutUpdate_NoCacheDoesNotWarn(t *testing.T) {
	// No cache on disk → no warning, even though we're on an older build.
	// (Background refresh may fire; that's fine — it doesn't print.)
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.0.1"

	t.Setenv("LTM_CACHE_DIR", t.TempDir())
	t.Setenv("LTM_NO_UPDATE_CHECK", "")
	// Block the detached refresh spawn so it doesn't touch real processes
	// (we can't easily stub os.Executable, but absence of cache is the key).

	out := captureStderr(t, func() {
		maybeWarnAboutUpdate(&cobra.Command{Use: "ls"})
	})
	if out != "" {
		t.Errorf("no cache should not warn, got: %q", out)
	}
}
