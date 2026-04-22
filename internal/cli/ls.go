package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// packetListRow is the minimal per-packet shape returned by GET /v1/packets.
type packetListRow struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Goal      string `json:"goal"`
}

// fetchPacketList calls GET /v1/packets?limit=N and returns both the raw JSON
// body (so callers can emit --json unchanged) and the decoded rows. Shared by
// `ltm ls` and the MCP `ls` tool so the two surfaces can never drift.
func fetchPacketList(cl *client, limit int) (raw []byte, rows []packetListRow, err error) {
	resp, err := cl.do("GET", fmt.Sprintf("/v1/packets?limit=%d", limit), nil)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if err := errFromResponse(resp); err != nil {
		return nil, nil, err
	}
	raw, _ = io.ReadAll(resp.Body)
	var parsed struct {
		Packets []packetListRow `json:"packets"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, nil, err
	}
	return raw, parsed.Packets, nil
}

func newLsCmd() *cobra.Command {
	var asJSON bool
	var limit int
	c := &cobra.Command{
		Use:   "ls",
		Short: "List recent packets on the server.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			raw, rows, err := fetchPacketList(cl, limit)
			if err != nil {
				return err
			}
			if asJSON {
				os.Stdout.Write(raw)
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tCREATED\tGOAL")
			for _, p := range rows {
				goal := p.Goal
				if len(goal) > 72 {
					goal = goal[:72] + "…"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.ID, p.CreatedAt, goal)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit raw JSON")
	c.Flags().IntVar(&limit, "limit", 50, "max packets to return")
	return c
}
