package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "show <id>",
		Short: "Pretty-print a packet by ID.",
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
			body, _ := io.ReadAll(resp.Body)

			if asJSON {
				os.Stdout.Write(body)
				os.Stdout.Write([]byte("\n"))
				return nil
			}
			out, err := formatPacketSummary(body)
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit raw JSON")
	return c
}

// formatPacketSummary turns a raw packet response body into the human-readable
// block `ltm show` prints. Shared with the MCP `show` tool so both surfaces
// render identical output.
func formatPacketSummary(body []byte) (string, error) {
	var p struct {
		ID            string   `json:"id"`
		CreatedAt     string   `json:"created_at"`
		LTMVersion    string   `json:"ltm_version"`
		Goal          string   `json:"goal"`
		NextStep      string   `json:"next_step"`
		Constraints   []string `json:"constraints"`
		OpenQuestions []string `json:"open_questions"`
		Tags          []string `json:"tags"`
		Project       *struct {
			Name string `json:"name"`
			Ref  string `json:"ref"`
		} `json:"project"`
		Decisions []struct {
			What   string `json:"what"`
			Why    string `json:"why"`
			Locked bool   `json:"locked"`
		} `json:"decisions"`
		Attempts []struct {
			Tried   string `json:"tried"`
			Outcome string `json:"outcome"`
			Learned string `json:"learned"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "ID         %s\n", p.ID)
	fmt.Fprintf(&b, "Created    %s\n", p.CreatedAt)
	fmt.Fprintf(&b, "Spec       v%s\n", p.LTMVersion)
	if p.Project != nil && (p.Project.Name != "" || p.Project.Ref != "") {
		fmt.Fprintf(&b, "Project    %s", p.Project.Name)
		if p.Project.Ref != "" {
			fmt.Fprintf(&b, " (%s)", p.Project.Ref)
		}
		fmt.Fprintln(&b)
	}
	if len(p.Tags) > 0 {
		fmt.Fprintf(&b, "Tags       %s\n", strings.Join(p.Tags, ", "))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Goal")
	fmt.Fprintln(&b, "  "+p.Goal)
	fmt.Fprintln(&b)
	if len(p.Constraints) > 0 {
		fmt.Fprintln(&b, "Constraints")
		for _, c := range p.Constraints {
			fmt.Fprintln(&b, "  - "+c)
		}
		fmt.Fprintln(&b)
	}
	if len(p.Decisions) > 0 {
		fmt.Fprintln(&b, "Decisions")
		for _, d := range p.Decisions {
			lock := ""
			if d.Locked {
				lock = " [locked]"
			}
			fmt.Fprintf(&b, "  - %s%s\n    why: %s\n", d.What, lock, d.Why)
		}
		fmt.Fprintln(&b)
	}
	if len(p.Attempts) > 0 {
		fmt.Fprintln(&b, "Attempts")
		for _, a := range p.Attempts {
			fmt.Fprintf(&b, "  - [%s] %s\n", a.Outcome, a.Tried)
			if a.Learned != "" {
				fmt.Fprintf(&b, "    learned: %s\n", a.Learned)
			}
		}
		fmt.Fprintln(&b)
	}
	if len(p.OpenQuestions) > 0 {
		fmt.Fprintln(&b, "Open questions")
		for _, q := range p.OpenQuestions {
			fmt.Fprintln(&b, "  - "+q)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "Next step")
	fmt.Fprintln(&b, "  "+p.NextStep)
	return b.String(), nil
}
