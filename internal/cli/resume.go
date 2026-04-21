package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/packet"
)

func newResumeCmd() *cobra.Command {
	var noCopy bool
	var forceCopy bool

	c := &cobra.Command{
		Use:   "resume [id]",
		Short: "Print a prompt-ready resume block for a Core Memory Packet.",
		Long: `Emits a ready-to-paste block an AI agent can consume to continue prior work.
Turns the packet's goal, locked decisions, prior attempts, open questions,
and next step into instructions the model will recognize on any platform.

With no ID, shows an interactive picker of recent packets
(arrow keys to navigate, enter to select). The resume block for
the selected packet is copied to the clipboard.

With an ID, writes the resume block to stdout — suitable for piping:

  ltm resume 01JDMCXK...    # stdout
  ltm resume 01JDMCXK... | pbcopy
  ltm resume                # interactive picker, clipboard copy`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}

			var id string
			interactive := len(args) == 0
			if interactive {
				id, err = pickPacketID(cl)
				if err != nil {
					return err
				}
				if id == "" {
					return nil
				}
			} else {
				id = args[0]
			}

			body, err := fetchPacketBody(cl, id)
			if err != nil {
				return err
			}
			p, err := packet.Parse(body)
			if err != nil {
				return fmt.Errorf("parse packet: %w", err)
			}

			block := renderResumeBlock(p)

			wantClipboard := (interactive || forceCopy) && !noCopy
			if wantClipboard {
				if err := clipboard.WriteAll(block); err != nil {
					fmt.Fprintln(os.Stderr, "warning: clipboard unavailable —", err)
					fmt.Print(block)
					return nil
				}
				fmt.Fprintf(os.Stderr, "✓ resume block copied to clipboard (%d chars, packet %s)\n", len(block), shortID(p.ID))
				return nil
			}
			fmt.Print(block)
			return nil
		},
	}
	c.Flags().BoolVar(&forceCopy, "copy", false, "copy to clipboard even when an ID is given")
	c.Flags().BoolVar(&noCopy, "no-copy", false, "always write to stdout, never touch the clipboard")
	return c
}

// ---- picker ----

func pickPacketID(cl *client) (string, error) {
	resp, err := cl.do("GET", "/v1/packets?limit=50", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := errFromResponse(resp); err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)

	var out struct {
		Packets []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
			Goal      string `json:"goal"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if len(out.Packets) == 0 {
		return "", errors.New("no packets on the server. push one first: ltm push <file>")
	}

	opts := make([]huh.Option[string], 0, len(out.Packets))
	for _, p := range out.Packets {
		rel := humanRelTime(p.CreatedAt)
		goal := collapseWS(p.Goal)
		if len(goal) > 80 {
			goal = goal[:79] + "…"
		}
		display := fmt.Sprintf("%-10s · %-12s · %s", shortID(p.ID), rel, goal)
		opts = append(opts, huh.NewOption(display, p.ID))
	}

	var chosen string
	err = huh.NewSelect[string]().
		Title("Resume which memory?").
		Description("↑ ↓ to navigate · ⏎ to select · esc to cancel").
		Options(opts...).
		Value(&chosen).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}
		return "", err
	}
	return chosen, nil
}

// ---- rendering ----

func renderResumeBlock(p *packet.Packet) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# Resume context — ltm Core Memory Packet")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "You are resuming prior work. The block below was written at the end of an earlier session by a previous agent/human pair. Treat it as authoritative: decisions marked locked are settled and must not be re-litigated; attempts listed here have already been tried and should not be repeated unless new information warrants it; the 'Next step' is your first action.")
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Goal")
	fmt.Fprintln(&b, p.Goal)

	if p.Project != nil && (p.Project.Name != "" || p.Project.Ref != "") {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Project")
		if p.Project.Name != "" {
			fmt.Fprintf(&b, "- Name: %s\n", p.Project.Name)
		}
		if p.Project.Ref != "" {
			fmt.Fprintf(&b, "- Ref:  %s\n", p.Project.Ref)
		}
	}

	if len(p.Constraints) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Constraints")
		for _, c := range p.Constraints {
			fmt.Fprintln(&b, "- "+c)
		}
	}

	if len(p.Decisions) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Decisions")
		for _, d := range p.Decisions {
			tag := "locked"
			if !d.Locked {
				tag = "tentative"
			}
			fmt.Fprintf(&b, "- [%s] %s\n  Rationale: %s\n", tag, d.What, d.Why)
		}
	}

	if len(p.Attempts) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Prior attempts (do not retry without new information)")
		for _, a := range p.Attempts {
			fmt.Fprintf(&b, "- [%s] %s\n", a.Outcome, a.Tried)
			if a.Learned != "" {
				fmt.Fprintf(&b, "  Learned: %s\n", a.Learned)
			}
		}
	}

	if len(p.OpenQuestions) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Open questions")
		for _, q := range p.OpenQuestions {
			fmt.Fprintln(&b, "- "+q)
		}
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Next step")
	fmt.Fprintln(&b, p.NextStep)

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "---")
	fmt.Fprintf(&b, "Packet ID:   %s\n", p.ID)
	fmt.Fprintf(&b, "Created:     %s\n", p.CreatedAt.UTC().Format(time.RFC3339))
	if p.Provenance != nil {
		parts := []string{}
		if p.Provenance.AuthorHuman != "" {
			parts = append(parts, p.Provenance.AuthorHuman)
		}
		if p.Provenance.AuthorModel != "" {
			parts = append(parts, "via "+p.Provenance.AuthorModel)
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "Author:      %s\n", strings.Join(parts, " "))
		}
	}
	if len(p.Tags) > 0 {
		fmt.Fprintf(&b, "Tags:        %s\n", strings.Join(p.Tags, ", "))
	}
	return b.String()
}

// ---- helpers ----

func fetchPacketBody(cl *client, id string) ([]byte, error) {
	resp, err := cl.do("GET", "/v1/packets/"+id, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := errFromResponse(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return "…" + id[len(id)-8:]
}

func humanRelTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%d min ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%d hr ago", h)
	case d < 7*24*time.Hour:
		dd := int(d.Hours() / 24)
		return fmt.Sprintf("%d d ago", dd)
	default:
		return t.Format("2006-01-02")
	}
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
