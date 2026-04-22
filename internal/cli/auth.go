package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/config"
)

// defaultHubURL is the managed ltm platform users hit when they just run
// `ltm auth` without arguments. Overridable with --host for staging/dev.
const defaultHubURL = "https://platform.ltm-cli.dev"

// cliClientID is the fixed uid of the first-party OAuth application seeded
// in ltm-hub for the device-authorization flow.
const cliClientID = "ltm-cli"

func newAuthCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auth [host [token]]",
		Short: "Authenticate against an ltm server.",
		Long: `Authenticate against an ltm server.

Three forms:

  ltm auth
      Sign in to the managed ltm platform via OAuth (device flow).
      Opens a browser, you confirm in the dashboard, token is stored
      locally — no copy-paste.

  ltm auth <host>
      Same device flow, but against a self-hosted ltm server.

  ltm auth <host> <token>
      Self-hosted servers that don't speak OAuth: paste a pre-issued
      bearer token directly. The token is written to
      ~/.config/ltm/credentials with 0600 perms.

Either way, the resolved host is saved as your default via 'ltm config set host'.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				return runDeviceFlow(defaultHubURL)
			case 1:
				return runDeviceFlow(strings.TrimRight(args[0], "/"))
			case 2:
				return runStaticTokenAuth(strings.TrimRight(args[0], "/"), args[1])
			}
			return nil
		},
	}
	c.AddCommand(&cobra.Command{
		Use:   "whoami",
		Short: "Show the current host and stored token fingerprint.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfg.Resolve()
			tok, _ := auth.LoadToken()
			fp := ""
			if tok != "" {
				fp = auth.HashToken(tok)[:8]
			}
			out := map[string]string{
				"host":        cfg.Host,
				"token_hash8": fp,
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	})
	return c
}

// runStaticTokenAuth handles the legacy `ltm auth <host> <token>` form used
// by self-hosted deployments that issue bearer tokens directly.
func runStaticTokenAuth(host, token string) error {
	hc := &http.Client{Timeout: 10 * time.Second}

	resp, err := hc.Get(host + "/v1/healthz")
	if err != nil {
		return fmt.Errorf("server unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected health response: %d", resp.StatusCode)
	}

	req, _ := http.NewRequest("GET", host+"/v1/packets?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp2, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("token check failed: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusUnauthorized {
		b, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("token rejected by server: %s", strings.TrimSpace(string(b)))
	}
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response: %d", resp2.StatusCode)
	}

	return persist(host, token)
}

// ---- device-authorization flow (RFC 8628) ----

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	// Some servers also return a pre-filled URL; we use it when present.
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

func runDeviceFlow(host string) error {
	hc := &http.Client{Timeout: 15 * time.Second}

	// Step 1 — request a device code.
	form := url.Values{}
	form.Set("client_id", cliClientID)
	form.Set("scope", "read write")

	resp, err := hc.PostForm(host+"/oauth/authorize_device", form)
	if err != nil {
		return fmt.Errorf("device-code request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("device-code request rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return fmt.Errorf("parse device-code response: %w", err)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" || dc.VerificationURI == "" {
		return fmt.Errorf("server returned an incomplete device-code response")
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	if dc.ExpiresIn <= 0 {
		dc.ExpiresIn = 300
	}

	browseURL := dc.VerificationURIComplete
	if browseURL == "" {
		browseURL = dc.VerificationURI
	}

	// Step 2 — prompt the user.
	fmt.Println()
	fmt.Println("Open this URL to connect your CLI:")
	fmt.Println("  ", dc.VerificationURI)
	fmt.Println()
	fmt.Println("Enter this code:")
	fmt.Println("  ", formatUserCode(dc.UserCode))
	fmt.Println()
	_ = openBrowser(browseURL)
	fmt.Println("Waiting for you to authorize…")

	// Step 3 — poll the token endpoint.
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	interval := time.Duration(dc.Interval) * time.Second

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("device code expired before authorization — run `ltm auth` again")
		}
		time.Sleep(interval)

		tok, err := pollToken(hc, host, dc.DeviceCode)
		if err != nil {
			return err
		}
		switch tok.Error {
		case "":
			if tok.AccessToken == "" {
				return fmt.Errorf("server returned success but no access_token")
			}
			return persist(host, tok.AccessToken)
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied":
			return fmt.Errorf("authorization denied")
		case "expired_token":
			return fmt.Errorf("device code expired before authorization — run `ltm auth` again")
		default:
			if tok.ErrorDesc != "" {
				return fmt.Errorf("authorization failed: %s (%s)", tok.Error, tok.ErrorDesc)
			}
			return fmt.Errorf("authorization failed: %s", tok.Error)
		}
	}
}

func pollToken(hc *http.Client, host, deviceCode string) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("device_code", deviceCode)
	form.Set("client_id", cliClientID)

	resp, err := hc.PostForm(host+"/oauth/token", form)
	if err != nil {
		return nil, fmt.Errorf("token poll failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("parse token response: %w (body: %s)", err, strings.TrimSpace(string(body)))
	}
	return &tok, nil
}

// persist writes the token + default host and prints the success message
// shared by both auth forms.
func persist(host, token string) error {
	if err := auth.SaveToken(token); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_ = cfg.Set("host", host)
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println("✓ authenticated")
	fmt.Println("✓ default host set to", host)
	return nil
}

// formatUserCode inserts a dash in the middle of 8-char device codes so
// they're easier to read out loud / type without losing our place.
func formatUserCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) == 8 {
		return code[:4] + "-" + code[4:]
	}
	return code
}

// openBrowser is best-effort — if we can't launch a browser we just print
// the URL and let the user copy it.
func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "linux":
		cmd = exec.Command("xdg-open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		return nil
	}
	return cmd.Start()
}
