package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

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
			path := fmt.Sprintf("/v1/packets?limit=%d", limit)
			resp, err := cl.do("GET", path, nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			body, _ := io.ReadAll(resp.Body)

			if asJSON {
				os.Stdout.Write(body)
				return nil
			}

			var parsed struct {
				Packets []struct {
					ID        string `json:"id"`
					CreatedAt string `json:"created_at"`
					Goal      string `json:"goal"`
				} `json:"packets"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tCREATED\tGOAL")
			for _, p := range parsed.Packets {
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
