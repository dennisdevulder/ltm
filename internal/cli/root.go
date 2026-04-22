package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/config"
	ltmschema "github.com/dennisdevulder/ltm/schema"
)

var Version = "0.1.0-dev"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "ltm",
		Short:        "Portable understanding for AI work sessions.",
		Long:         "ltm moves the intent and state of a work session between machines, models, and agents — without dragging along your configuration.",
		Version:      fmt.Sprintf("%s (protocol %s)", Version, ltmschema.Current),
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			maybeWarnAboutUpdate(cmd)
		},
	}
	root.AddCommand(
		newConfigCmd(),
		newAuthCmd(),
		newPushCmd(),
		newPullCmd(),
		newLsCmd(),
		newShowCmd(),
		newRmCmd(),
		newResumeCmd(),
		newExampleCmd(),
		newUpdateCmd(),
		newServerCmd(),
	)
	return root
}

// ---- shared: HTTP client against the configured server ----

type client struct {
	host   string
	token  string
	http   *http.Client
}

func newClient() (*client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	cfg.Resolve()
	if cfg.Host == "" {
		return nil, fmt.Errorf("no host configured. run: ltm config set host <url>")
	}
	tok, err := auth.LoadToken()
	if err != nil {
		return nil, fmt.Errorf("not authenticated. run: ltm auth <url> <token>")
	}
	return &client{
		host:  cfg.Host,
		token: tok,
		http:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *client) do(method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(method, c.host+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// errFromResponse reads the body and returns a descriptive error if the status is not 2xx.
func errFromResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := string(bytes.TrimSpace(b))
	var parsed map[string]any
	if json.Unmarshal(b, &parsed) == nil {
		if e, ok := parsed["error"].(string); ok {
			msg = e
		}
	}
	return fmt.Errorf("server returned %d: %s", resp.StatusCode, msg)
}
