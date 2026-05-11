package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newPublishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "publish <id>",
		Short: "Publish a packet to a public URL anyone can view (no account required).",
		Long: "Publish a packet so anyone with the link can view and copy it from a browser.\n" +
			"No account is required to view a published packet. The URL is unguessable\n" +
			"(it embeds the packet's ULID). Run 'ltm unpublish <id>' to revoke access.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("POST", "/v1/packets/"+args[0]+"/publish", nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			body, _ := io.ReadAll(resp.Body)
			var parsed struct {
				ID        string `json:"id"`
				PublicURL string `json:"public_url"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				return fmt.Errorf("decode publish response: %w", err)
			}
			if parsed.PublicURL != "" {
				fmt.Println(parsed.PublicURL)
			} else {
				fmt.Println("published", parsed.ID)
			}
			return nil
		},
	}
}

func newUnpublishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unpublish <id>",
		Short: "Revoke public access to a previously published packet.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("DELETE", "/v1/packets/"+args[0]+"/publish", nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			fmt.Println("unpublished", args[0])
			return nil
		},
	}
}
