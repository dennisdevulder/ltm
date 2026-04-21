package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/config"
)

func newAuthCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auth <host> <token>",
		Short: "Authenticate against an ltm server.",
		Long: `Store credentials for a self-hosted ltm server.
Usage:
  ltm auth https://ltm.my-vps.dev  <paste-token>

The token is written to ~/.config/ltm/credentials with 0600 perms.
The host is saved as your default via 'ltm config set host'.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := strings.TrimRight(args[0], "/")
			token := args[1]

			// quick sanity check: /v1/healthz should work without auth
			hc := &http.Client{Timeout: 10 * time.Second}
			resp, err := hc.Get(host + "/v1/healthz")
			if err != nil {
				return fmt.Errorf("server unreachable: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("unexpected health response: %d", resp.StatusCode)
			}

			// try the token against a real endpoint
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

			// all good — persist
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
