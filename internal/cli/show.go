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
				return err
			}

			fmt.Printf("ID         %s\n", p.ID)
			fmt.Printf("Created    %s\n", p.CreatedAt)
			fmt.Printf("Spec       v%s\n", p.LTMVersion)
			if p.Project != nil && (p.Project.Name != "" || p.Project.Ref != "") {
				fmt.Printf("Project    %s", p.Project.Name)
				if p.Project.Ref != "" {
					fmt.Printf(" (%s)", p.Project.Ref)
				}
				fmt.Println()
			}
			if len(p.Tags) > 0 {
				fmt.Printf("Tags       %s\n", strings.Join(p.Tags, ", "))
			}
			fmt.Println()
			fmt.Println("Goal")
			fmt.Println("  " + p.Goal)
			fmt.Println()
			if len(p.Constraints) > 0 {
				fmt.Println("Constraints")
				for _, c := range p.Constraints {
					fmt.Println("  - " + c)
				}
				fmt.Println()
			}
			if len(p.Decisions) > 0 {
				fmt.Println("Decisions")
				for _, d := range p.Decisions {
					lock := ""
					if d.Locked {
						lock = " [locked]"
					}
					fmt.Printf("  - %s%s\n    why: %s\n", d.What, lock, d.Why)
				}
				fmt.Println()
			}
			if len(p.Attempts) > 0 {
				fmt.Println("Attempts")
				for _, a := range p.Attempts {
					fmt.Printf("  - [%s] %s\n", a.Outcome, a.Tried)
					if a.Learned != "" {
						fmt.Printf("    learned: %s\n", a.Learned)
					}
				}
				fmt.Println()
			}
			if len(p.OpenQuestions) > 0 {
				fmt.Println("Open questions")
				for _, q := range p.OpenQuestions {
					fmt.Println("  - " + q)
				}
				fmt.Println()
			}
			fmt.Println("Next step")
			fmt.Println("  " + p.NextStep)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit raw JSON")
	return c
}
