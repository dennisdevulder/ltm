package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---- pure helpers ----

func TestStripV(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"v", ""},
		{"v0.1.0", "0.1.0"},
		{"0.1.0", "0.1.0"},
		{"  v0.2.0  ", "0.2.0"},
		{"\tv1.2.3\n", "1.2.3"},
		{"vv0.1.0", "v0.1.0"}, // only strips one leading 'v'
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := stripV(tc.in); got != tc.want {
				t.Errorf("stripV(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatCurrent(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "(unknown)"},
		{"0.1.0", "v0.1.0"},
		{"1.2.3-beta", "v1.2.3-beta"},
	}
	for _, tc := range cases {
		if got := formatCurrent(tc.in); got != tc.want {
			t.Errorf("formatCurrent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSha256hex(t *testing.T) {
	// sha256("") — well-known value. Locks in the algorithm so an accidental
	// change to a different hash function would be caught immediately.
	got := sha256hex(nil)
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("sha256hex(nil) = %q, want %q", got, want)
	}
	// Output is lowercase hex and 64 chars.
	got = sha256hex([]byte("hello"))
	if len(got) != 64 {
		t.Errorf("sha256hex returned %d chars, want 64", len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Errorf("sha256hex output not valid hex: %v", err)
	}
}

func TestFindChecksum(t *testing.T) {
	// Typical checksums.txt layout: "<hex>  <filename>", one per line.
	file := []byte(`
abc123  ltm_0.1.0_linux_amd64.tar.gz
def456  ltm_0.1.0_darwin_arm64.tar.gz
# comments are ignored because Fields yields len != 2
789xyz  ltm_0.1.0_windows_amd64.zip
`)
	cases := []struct {
		name     string
		filename string
		want     string
	}{
		{"found-linux", "ltm_0.1.0_linux_amd64.tar.gz", "abc123"},
		{"found-darwin", "ltm_0.1.0_darwin_arm64.tar.gz", "def456"},
		{"not-present", "ltm_9.9.9_nosuch.tar.gz", ""},
		{"empty-filename", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findChecksum(file, tc.filename); got != tc.want {
				t.Errorf("findChecksum(%q) = %q, want %q", tc.filename, got, tc.want)
			}
		})
	}
}

func TestFindChecksum_EmptyInput(t *testing.T) {
	if got := findChecksum(nil, "anything"); got != "" {
		t.Errorf("findChecksum(nil, _) = %q, want empty", got)
	}
	if got := findChecksum([]byte(""), "anything"); got != "" {
		t.Errorf("findChecksum(empty, _) = %q, want empty", got)
	}
}

// ---- canWrite ----

func TestCanWrite_WritableDir(t *testing.T) {
	dir := t.TempDir()
	if !canWrite(dir) {
		t.Errorf("canWrite should return true for tempdir %q", dir)
	}
}

func TestCanWrite_NonexistentDir(t *testing.T) {
	if canWrite("/this/path/does/not/exist/at/all") {
		t.Error("canWrite should return false for nonexistent dir")
	}
}

func TestCanWrite_ReadOnlyDir(t *testing.T) {
	// Skip on platforms where chmod(0o500) doesn't prevent creation for the
	// owner (notably: when running as root, or on Windows).
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode bits")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(dir, 0o700) // restore so TempDir cleanup works
	if canWrite(dir) {
		t.Error("canWrite should return false for r-only dir")
	}
}

// ---- extractLTMBinary ----

// writeTarGz builds a minimal tar.gz in memory with the given files. Used by
// extract tests so we don't have to ship fixture binaries.
func writeTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractLTMBinary_FindsBinary(t *testing.T) {
	payload := []byte("fake-binary-bytes")
	archive := writeTarGz(t, map[string][]byte{
		"README.md": []byte("readme"),
		"ltm":       payload,
	})
	got, err := extractLTMBinary(archive)
	if err != nil {
		t.Fatalf("extractLTMBinary: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("binary mismatch: got %q, want %q", got, payload)
	}
}

func TestExtractLTMBinary_FindsBinaryInSubdir(t *testing.T) {
	// extractLTMBinary uses filepath.Base, so a binary under a subdirectory
	// should still be found. This matters for release archives that ship
	// with a versioned folder layout.
	payload := []byte("nested-binary")
	archive := writeTarGz(t, map[string][]byte{
		"dist/ltm": payload,
	})
	got, err := extractLTMBinary(archive)
	if err != nil {
		t.Fatalf("extractLTMBinary: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("binary mismatch: got %q, want %q", got, payload)
	}
}

func TestExtractLTMBinary_MissingBinary(t *testing.T) {
	archive := writeTarGz(t, map[string][]byte{
		"README.md":    []byte("readme"),
		"checksums.txt": []byte("abc"),
	})
	_, err := extractLTMBinary(archive)
	if err == nil {
		t.Fatal("expected error when 'ltm' binary is absent, got nil")
	}
	if !strings.Contains(err.Error(), "no 'ltm' binary") {
		t.Errorf("expected 'no ltm binary' error, got: %v", err)
	}
}

func TestExtractLTMBinary_BadGzip(t *testing.T) {
	_, err := extractLTMBinary([]byte("not-a-gzip-archive"))
	if err == nil {
		t.Error("expected error for non-gzip input, got nil")
	}
}

// ---- httpGetBytes / fetchLatestReleaseTag against httptest server ----

func TestHttpGetBytes_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello, world"))
	}))
	defer srv.Close()

	got, err := httpGetBytes(srv.URL, 1024)
	if err != nil {
		t.Fatalf("httpGetBytes: %v", err)
	}
	if string(got) != "hello, world" {
		t.Errorf("httpGetBytes = %q, want %q", got, "hello, world")
	}
}

func TestHttpGetBytes_RespectsByteCap(t *testing.T) {
	// maxBytes caps the response reader — any bytes past the cap are dropped.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 500))
	}))
	defer srv.Close()

	got, err := httpGetBytes(srv.URL, 10)
	if err != nil {
		t.Fatalf("httpGetBytes: %v", err)
	}
	if len(got) != 10 {
		t.Errorf("got %d bytes, want 10 (cap)", len(got))
	}
}

func TestHttpGetBytes_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 404)
	}))
	defer srv.Close()

	_, err := httpGetBytes(srv.URL, 1024)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected '404' in error, got: %v", err)
	}
}

func TestHttpGetBytes_NetworkError(t *testing.T) {
	_, err := httpGetBytes("http://127.0.0.1:1", 1024) // nothing listening
	if err == nil {
		t.Error("expected error when connection refused, got nil")
	}
}

// ---- update-check cache integration ----

func TestRunUpdate_CheckOnly_ReportsUpToDate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("self-update disabled on Windows")
	}

	// Stash and restore Version so this test is hermetic.
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.1.0"

	// isolate the update-check cache dir so writeUpdateCheckCache doesn't
	// hit a real ~/.cache/ltm path.
	t.Setenv("LTM_CACHE_DIR", t.TempDir())

	// target="0.1.0" short-circuits the GitHub fetch.
	if err := runUpdate(true, false, "0.1.0"); err != nil {
		t.Fatalf("runUpdate --check: %v", err)
	}
}

func TestRunUpdate_CheckOnly_ReportsAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.0.9"
	t.Setenv("LTM_CACHE_DIR", t.TempDir())

	// checkOnly path should succeed even when newer is available.
	if err := runUpdate(true, false, "0.1.0"); err != nil {
		t.Errorf("runUpdate --check with available update: %v", err)
	}
}

func TestRunUpdate_NoOp_WhenAtLatestWithoutForce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })
	Version = "0.1.0"
	t.Setenv("LTM_CACHE_DIR", t.TempDir())

	// checkOnly=false, force=false, current==latest — should return nil
	// without attempting a download (which would need network).
	if err := runUpdate(false, false, "0.1.0"); err != nil {
		t.Errorf("runUpdate no-op path: %v", err)
	}
}

func TestRunUpdate_WindowsBlocked(t *testing.T) {
	// The windows branch is behind runtime.GOOS, so on non-windows we can
	// only assert we don't regress the error text shape. Test the pure
	// helpers above keeps coverage high; this test only runs on Windows.
	if runtime.GOOS != "windows" {
		t.Skip("only meaningful on windows")
	}
	err := runUpdate(true, false, "0.1.0")
	if err == nil || !strings.Contains(err.Error(), "not supported on Windows") {
		t.Errorf("expected windows error, got: %v", err)
	}
}

// ---- fetchLatestReleaseTag via httptest ----

// fetchLatestReleaseTag hardcodes api.github.com, so we can only cover its
// success path by swapping the URL. Since we can't do that without code
// changes, we settle for covering the helpers it uses.

func TestExtractLTMBinary_EndToEnd(t *testing.T) {
	// Round-trip sha256 → extract, to exercise the same pipeline
	// downloadAndInstall runs between the checksum check and disk write.
	payload := bytes.Repeat([]byte("ltm"), 200)
	archive := writeTarGz(t, map[string][]byte{"ltm": payload})
	sum := sha256hex(archive)
	checksums := []byte(sum + "  ltm_0.1.0_test_test.tar.gz\n")
	if got := findChecksum(checksums, "ltm_0.1.0_test_test.tar.gz"); got != sum {
		t.Errorf("checksum mismatch: got %q, want %q", got, sum)
	}
	bin, err := extractLTMBinary(archive)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bytes.Equal(bin, payload) {
		t.Errorf("binary roundtrip mismatch")
	}
	// Confirm a tampered archive is caught by checksum comparison.
	tampered := append([]byte{}, archive...)
	tampered[len(tampered)-1] ^= 0xFF
	if sha256hex(tampered) == sum {
		t.Error("tampered archive matched original checksum (impossible)")
	}
}

// ---- canWrite smoke + realpath resolution ----

func TestCanWrite_SymlinkDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if !canWrite(link) {
		t.Errorf("canWrite via symlink should succeed")
	}
}
