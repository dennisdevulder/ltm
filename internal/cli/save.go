package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSaveCmd() *cobra.Command {
	var allowUnredacted bool
	var lenient bool
	var team string

	c := &cobra.Command{
		Use:   "save [file | -]",
		Short: "Save the current session as a packet and push it.",
		Long: `Reads a Core Memory Packet and pushes it to the configured server in one step.

Agents with the ltm MCP wired up should call the 'save' tool instead —
the packet is passed inline as a JSON argument and never touches disk.

In non-MCP contexts, have the agent write the packet to a file (typically
under /tmp) and run:

  ltm save /tmp/packet.json
  ltm save -              # read from stdin

Same validation and redaction pre-flight as 'ltm push'.`,
		Args: cobra.ExactArgs(1),
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
	c.Flags().StringVarP(&team, "team", "t", "", "save into this team instead of personal scope")
	return c
}
