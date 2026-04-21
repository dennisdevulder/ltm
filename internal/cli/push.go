package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/packet"
)

func newPushCmd() *cobra.Command {
	var allowUnredacted bool
	var lenient bool

	c := &cobra.Command{
		Use:   "push [file | -]",
		Short: "Send a packet to your server. Use '-' to read from stdin.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// read input
			var raw []byte
			var err error
			if args[0] == "-" {
				raw, err = io.ReadAll(os.Stdin)
			} else {
				raw, err = os.ReadFile(args[0])
			}
			if err != nil {
				return err
			}

			// validate
			if !lenient {
				if err := packet.Validate(raw); err != nil {
					return fmt.Errorf("packet rejected: %w", err)
				}
			}

			p, err := packet.Parse(raw)
			if err != nil && !lenient {
				return err
			}

			// redact pre-flight
			if p != nil && !allowUnredacted {
				if issues := packet.Redact(p); len(issues) > 0 {
					fmt.Fprintln(os.Stderr, "packet contains redactable content — refusing to push.")
					for _, i := range issues {
						fmt.Fprintf(os.Stderr, "  - %s\n", i.String())
					}
					fmt.Fprintln(os.Stderr, "pass --allow-unredacted to override (not recommended).")
					return fmt.Errorf("redaction failed")
				}
			}

			// send
			cl, err := newClient()
			if err != nil {
				return err
			}
			resp, err := cl.do("POST", "/v1/packets", raw)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if err := errFromResponse(resp); err != nil {
				return err
			}
			fmt.Println(p.ID)
			return nil
		},
	}
	c.Flags().BoolVar(&allowUnredacted, "allow-unredacted", false, "skip the redaction pre-flight")
	c.Flags().BoolVar(&lenient, "lenient", false, "skip schema validation")
	return c
}
