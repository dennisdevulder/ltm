package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/packet"
)

func newPushCmd() *cobra.Command {
	var allowUnredacted bool
	var lenient bool
	var team string

	c := &cobra.Command{
		Use:   "push [file | -]",
		Short: "Send a packet to your server. Use '-' to read from stdin.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readPacketInput(args[0])
			if err != nil {
				return err
			}
			id, err := validateAndPushPacket(raw, lenient, allowUnredacted, team)
			if err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	c.Flags().BoolVar(&allowUnredacted, "allow-unredacted", false, "skip the redaction pre-flight")
	c.Flags().BoolVar(&lenient, "lenient", false, "skip schema validation")
	c.Flags().StringVarP(&team, "team", "t", "", "push into this team instead of personal scope")
	return c
}

// readPacketInput reads a packet body from a file path or from stdin when arg == "-".
func readPacketInput(arg string) ([]byte, error) {
	if arg == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(arg)
}

// validateAndPushPacket runs the same validate → redact → POST pipeline that
// push and save share. lenient skips schema validation; allowUnredacted skips
// the secret/abs-path pre-flight. team routes the packet into a team's scope
// ("" = personal). Returns the server-assigned packet ID.
func validateAndPushPacket(raw []byte, lenient, allowUnredacted bool, team string) (string, error) {
	if !lenient {
		if err := packet.Validate(raw); err != nil {
			return "", fmt.Errorf("packet rejected: %w", err)
		}
	}

	p, err := packet.Parse(raw)
	if err != nil && !lenient {
		return "", err
	}

	if p != nil && !allowUnredacted {
		if issues := packet.Redact(p); len(issues) > 0 {
			fmt.Fprintln(os.Stderr, "packet contains redactable content — refusing to push.")
			for _, i := range issues {
				fmt.Fprintf(os.Stderr, "  - %s\n", i.String())
			}
			fmt.Fprintln(os.Stderr, "pass --allow-unredacted to override (not recommended).")
			return "", fmt.Errorf("redaction failed")
		}
	}

	cl, err := newClient()
	if err != nil {
		return "", err
	}
	path := "/v1/packets"
	if team != "" {
		path += "?team=" + url.QueryEscape(team)
	}
	resp, err := cl.do("POST", path, raw)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := errFromResponse(resp); err != nil {
		return "", err
	}
	if p == nil {
		return "", nil
	}
	return p.ID, nil
}
