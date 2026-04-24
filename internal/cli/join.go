package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/config"
)

// configuredHost returns the currently configured host, if any.
func configuredHost() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	cfg.Resolve()
	if cfg.Host == "" {
		return "", fmt.Errorf("no host configured. run: ltm auth <host> <token>")
	}
	return cfg.Host, nil
}

// redeemInvite POSTs /v1/invites/<code>/accept. It attaches the existing
// bearer token when one is already stored (so the current user joins the
// team) and otherwise redeems anonymously, persisting the token the server
// mints back.
func redeemInvite(host, code string) error {
	host = strings.TrimRight(host, "/")
	hc := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest("POST", host+"/v1/invites/"+code+"/accept", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if existing, err := auth.LoadToken(); err == nil && existing != "" {
		req.Header.Set("Authorization", "Bearer "+existing)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("invite-accept request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode == http.StatusGone {
		return fmt.Errorf("invite expired or already consumed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := string(bytes.TrimSpace(body))
		var parsed map[string]any
		if json.Unmarshal(body, &parsed) == nil {
			if e, ok := parsed["error"].(string); ok {
				msg = e
			}
		}
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, msg)
	}
	var parsed struct {
		Token string `json:"token"`
		User  struct {
			ID      string `json:"id"`
			Display string `json:"display"`
		} `json:"user"`
		Team struct {
			Name string `json:"name"`
		} `json:"team"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("parse accept response: %w", err)
	}
	if parsed.Token != "" {
		// First-time redemption on a clean machine — persist both host and
		// token so subsequent commands work without running `ltm auth`.
		if err := auth.SaveToken(parsed.Token); err != nil {
			return err
		}
		cfg, _ := config.Load()
		_ = cfg.Set("host", host)
		_ = cfg.Save()
		fmt.Println("✓ authenticated")
		fmt.Println("✓ default host set to", host)
	}
	fmt.Printf("✓ joined team %s\n", parsed.Team.Name)
	return nil
}
