package cli

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/packet"
)

//go:embed examples/example.v0.2.json
var embeddedExamplePacket []byte

func newExampleCmd() *cobra.Command {
	var resume bool

	c := &cobra.Command{
		Use:   "example",
		Short: "Print an embedded sample Core Memory Packet (offline, no server).",
		Long: `Emits a hand-written v0.2 Core Memory Packet bundled into the binary.
Useful as a first-run demo: see what a real packet looks like without
needing to push or pull anything from a server.

The packet's goal points at github.com/dennisdevulder/ltm-example, a
deliberately dry email.md the receiving agent is meant to clone and
rewrite. Pipe the resume block straight into your agent:

  ltm example                  # raw JSON (validates against v0.2 schema)
  ltm example --resume         # prompt-ready resume block
  ltm example --resume | pbcopy
  ltm example | ltm push -     # ship it to your server, if you want`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := packet.Parse(embeddedExamplePacket)
			if err != nil {
				return fmt.Errorf("embedded example packet is invalid: %w", err)
			}
			if resume {
				fmt.Print(renderResumeBlock(p))
				return nil
			}
			cmd.OutOrStdout().Write(embeddedExamplePacket)
			return nil
		},
	}
	c.Flags().BoolVar(&resume, "resume", false, "render a prompt-ready resume block instead of raw JSON")
	return c
}
