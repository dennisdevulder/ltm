package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/config"
)

// browserOpener is the system-browser launcher used by `ltm platform`.
// It's a var so tests can replace it without spawning a real browser.
var browserOpener = openBrowser

// platformDashboardURL returns the managed dashboard URL or an error
// explaining why the current configuration can't reach one. Shared by the
// `ltm platform` CLI command and the `platform` MCP tool so the two stay
// in lock-step.
func platformDashboardURL() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	cfg.Resolve()
	switch {
	case cfg.Host == "":
		return "", fmt.Errorf("no host configured. run 'ltm auth' to sign in to the managed platform")
	case cfg.Host != defaultHubURL:
		return "", fmt.Errorf("'ltm platform' opens the managed dashboard at %s, but you are configured against %s (self-hosted). open your own dashboard directly", defaultHubURL, cfg.Host)
	}
	return defaultHubURL, nil
}

func newPlatformCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "platform",
		Aliases: []string{"dashboard"},
		Short:   "Open the managed ltm platform dashboard in your browser.",
		Long: "Open the managed ltm platform dashboard at " + defaultHubURL + " in your default browser.\n\n" +
			"Only works when you are signed in to the managed platform. Self-hosted users manage their own dashboard.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := platformDashboardURL()
			if err != nil {
				return err
			}
			fmt.Println(target)
			_ = browserOpener(target)
			return nil
		},
	}
}

func toolPlatform() (string, error) {
	return platformDashboardURL()
}
