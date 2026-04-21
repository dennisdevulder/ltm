package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const updateRepo = "dennisdevulder/ltm"

func newUpdateCmd() *cobra.Command {
	var checkOnly bool
	var force bool
	var target string

	c := &cobra.Command{
		Use:   "update",
		Short: "Update ltm to the latest release.",
		Long: `Fetch the latest ltm release from GitHub, verify its checksum,
and atomically replace the running binary.

  ltm update                    # update to latest
  ltm update --check            # report latest; don't install
  ltm update --force            # reinstall even if already up to date
  ltm update --version v0.1.0   # install a specific version`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(checkOnly, force, target)
		},
	}
	c.Flags().BoolVar(&checkOnly, "check", false, "report latest version; don't install")
	c.Flags().BoolVar(&force, "force", false, "reinstall even if already up to date")
	c.Flags().StringVar(&target, "version", "", "install a specific version instead of latest")
	return c
}

func runUpdate(checkOnly, force bool, target string) error {
	if runtime.GOOS == "windows" {
		return errors.New("self-update not supported on Windows; reinstall from the releases page")
	}

	current := stripV(strings.TrimSpace(Version))

	latest := stripV(target)
	if latest == "" {
		tag, err := fetchLatestReleaseTag()
		if err != nil {
			return fmt.Errorf("check latest release: %w", err)
		}
		latest = stripV(tag)
		writeUpdateCheckCache(tag)
	}

	if checkOnly {
		fmt.Printf("current: v%s\nlatest:  v%s\n", current, latest)
		if current == latest {
			fmt.Println("status:  up to date")
		} else {
			fmt.Println("status:  update available — run 'ltm update' to install")
		}
		return nil
	}

	if !force && current == latest {
		fmt.Printf("ltm v%s is already the latest release.\n", current)
		return nil
	}

	if err := downloadAndInstall(latest); err != nil {
		return err
	}
	fmt.Printf("✓ updated %s → v%s\n", formatCurrent(current), latest)
	return nil
}

func formatCurrent(v string) string {
	if v == "" {
		return "(unknown)"
	}
	return "v" + v
}

func stripV(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "v") }

// ---- download pipeline ----

func downloadAndInstall(version string) error {
	archiveName := fmt.Sprintf("ltm_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	archiveURL := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", updateRepo, version, archiveName)
	checksumURL := fmt.Sprintf("https://github.com/%s/releases/download/v%s/checksums.txt", updateRepo, version)

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}
	installDir := filepath.Dir(realPath)

	if !canWrite(installDir) {
		return fmt.Errorf("%s is not writable. rerun with elevated permissions:\n  sudo -E ltm update", installDir)
	}

	fmt.Printf("==> downloading %s\n", archiveName)
	archive, err := httpGetBytes(archiveURL, 64*1024*1024)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	fmt.Println("==> verifying checksum")
	checksums, err := httpGetBytes(checksumURL, 64*1024)
	if err != nil {
		return fmt.Errorf("fetch checksums: %w", err)
	}
	want := findChecksum(checksums, archiveName)
	if want == "" {
		return fmt.Errorf("no entry for %s in checksums.txt", archiveName)
	}
	got := sha256hex(archive)
	if got != want {
		return fmt.Errorf("checksum mismatch for %s\n  want: %s\n  got:  %s", archiveName, want, got)
	}

	fmt.Println("==> extracting")
	bin, err := extractLTMBinary(archive)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	tmp, err := os.CreateTemp(installDir, ".ltm-update-*")
	if err != nil {
		return fmt.Errorf("stage binary: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	fmt.Printf("==> installing to %s\n", realPath)
	if err := os.Rename(tmpPath, realPath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

// ---- helpers ----

type ghRelease struct {
	TagName string `json:"tag_name"`
}

func fetchLatestReleaseTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", updateRepo)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github API returned %d", resp.StatusCode)
	}
	var r ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", errors.New("no tag_name in GitHub release response")
	}
	return r.TagName, nil
}

func httpGetBytes(url string, maxBytes int64) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func findChecksum(checksumFile []byte, filename string) string {
	for _, line := range strings.Split(string(checksumFile), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return fields[0]
		}
	}
	return ""
}

func extractLTMBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no 'ltm' binary in archive")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "ltm" {
			return io.ReadAll(tr)
		}
	}
}

func canWrite(dir string) bool {
	f, err := os.CreateTemp(dir, ".ltm-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
