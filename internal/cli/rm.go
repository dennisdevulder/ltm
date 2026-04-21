package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a packet by ID.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("DELETE", "/v1/packets/"+args[0], nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			fmt.Println("deleted", args[0])
			return nil
		},
	}
}
