package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// teamRow mirrors the JSON shape returned by GET /v1/teams.
type teamRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"`
	CreatedAt string `json:"created_at"`
}

type memberRow struct {
	UserID   string `json:"user_id"`
	Display  string `json:"display"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	JoinedAt string `json:"joined_at"`
}

func newTeamsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "teams",
		Short: "Manage server-scoped teams and their membership.",
	}
	c.AddCommand(newTeamsCreateCmd())
	c.AddCommand(newTeamsRmCmd())
	c.AddCommand(newTeamsLsCmd())
	c.AddCommand(newTeamsMembersCmd())
	c.AddCommand(newTeamsLeaveCmd())
	return c
}

func newTeamsCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a team. The caller becomes owner.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]string{"name": args[0]})
			resp, err := cl.do("POST", "/v1/teams", body)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			fmt.Println("created team", args[0])
			return nil
		},
	}
}

func newTeamsRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a team (owner only). Also deletes the team's packets.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("DELETE", "/v1/teams/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			fmt.Println("deleted team", args[0])
			return nil
		},
	}
}

func newTeamsLsCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "ls",
		Short: "List teams the caller is a member of.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("GET", "/v1/teams", nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			raw, _ := io.ReadAll(resp.Body)
			if asJSON {
				os.Stdout.Write(raw)
				return nil
			}
			var parsed struct {
				Teams []teamRow `json:"teams"`
			}
			if err := json.Unmarshal(raw, &parsed); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tCREATED\tOWNER")
			for _, t := range parsed.Teams {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, t.CreatedAt, t.OwnerID)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit raw JSON")
	return c
}

func newTeamsMembersCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "members <name>",
		Short: "List a team's members.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("GET", "/v1/teams/"+url.PathEscape(args[0])+"/members", nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			raw, _ := io.ReadAll(resp.Body)
			if asJSON {
				os.Stdout.Write(raw)
				return nil
			}
			var parsed struct {
				Members []memberRow `json:"members"`
			}
			if err := json.Unmarshal(raw, &parsed); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "DISPLAY\tROLE\tJOINED")
			for _, m := range parsed.Members {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Display, m.Role, m.JoinedAt)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit raw JSON")
	return c
}

func newTeamsLeaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "leave <name>",
		Short: "Leave a team. Owners must transfer or delete first.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("POST", "/v1/teams/"+url.PathEscape(args[0])+"/leave", []byte("{}"))
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			fmt.Println("left team", args[0])
			return nil
		},
	}
}

// newInviteCmd implements `ltm invite -t <name>` — mints a one-time invite URL.
func newInviteCmd() *cobra.Command {
	var team string
	c := &cobra.Command{
		Use:   "invite -t <name>",
		Short: "Mint a one-time invite URL for a team.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if team == "" {
				return fmt.Errorf("-t <team> is required")
			}
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("POST", "/v1/teams/"+url.PathEscape(team)+"/invites", []byte("{}"))
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			var parsed struct {
				Code      string `json:"code"`
				URL       string `json:"url"`
				ExpiresAt string `json:"expires_at"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
				return err
			}
			fmt.Println(parsed.URL)
			fmt.Fprintf(os.Stderr, "expires: %s\n", parsed.ExpiresAt)
			return nil
		},
	}
	c.Flags().StringVarP(&team, "team", "t", "", "team name (required)")
	return c
}

// newJoinCmd implements `ltm join <url-or-code>`. On a machine with no prior
// auth the server mints a fresh token and we persist it plus the host.
func newJoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join <url-or-code>",
		Short: "Redeem an invite on the current server.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host, code, err := parseInvite(args[0])
			if err != nil {
				return err
			}
			// Use an existing client only when the caller already has auth
			// configured. Otherwise we talk to the host directly — the
			// invite-accept endpoint is unauthenticated by design.
			return redeemInvite(host, code)
		},
	}
}

// parseInvite accepts either a full `<host>/v1/invites/<code>` URL or a bare
// code. A bare code requires the host to already be in config.
func parseInvite(input string) (host, code string, err error) {
	const marker = "/v1/invites/"
	if idx := strings.Index(input, marker); idx >= 0 {
		return input[:idx], input[idx+len(marker):], nil
	}
	h, err := configuredHost()
	if err != nil {
		return "", "", fmt.Errorf("plain invite code requires a configured host: %w", err)
	}
	return h, input, nil
}
