package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull <id>",
		Short: "Fetch a packet by ID and write it to stdout.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("GET", "/v1/packets/"+args[0], nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			_, err = io.Copy(os.Stdout, resp.Body)
			os.Stdout.Write([]byte("\n"))
			return err
		},
	}
}
