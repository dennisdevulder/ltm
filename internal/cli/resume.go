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
	"github.com/charmbracelet/bubbles/key"
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
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Resume which memory?").
				Description("↑ ↓ to navigate · ⏎ to select · esc to cancel").
				Options(opts...).
				Value(&chosen),
		),
	).WithKeyMap(pickerKeyMap()).Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}
		return "", err
	}
	return chosen, nil
}

// pickerKeyMap returns huh's default keymap with Quit widened to include esc
// so the picker can be cancelled without Ctrl+C. huh v1.0.0 binds Quit to
// Ctrl+C only by default; esc is otherwise bound to disabled filter actions
// on Select and is silently swallowed.
func pickerKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(
		key.WithKeys("ctrl+c", "esc"),
		key.WithHelp("esc", "cancel"),
	)
	return km
}

// ---- rendering ----

// renderResumeBlock produces the markdown block pasted into an agent's
// opening prompt. Ordering follows Liu et al. 2023 ("Lost in the Middle"):
// the highest-priority fields (goal, locked decisions, failed attempts,
// next_step) are placed at the start and end of the block; lower-priority
// context (constraints, methods, tentative decisions, open questions) sits
// in the middle where model attention sags.
func renderResumeBlock(p *packet.Packet) string {
	var b strings.Builder

	fmt.Fprintln(&b, "# Resume context — ltm Core Memory Packet")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "You are resuming prior work. Treat this block as authoritative:")
	fmt.Fprintln(&b, "- Decisions marked **locked** are settled; do not re-litigate them.")
	fmt.Fprintln(&b, "- Attempts listed as failed have already been tried and must not be retried unless new information warrants it.")
	fmt.Fprintln(&b, "- 'Next step' is your first action.")
	fmt.Fprintln(&b)

	// ---- START (high-attention) ----

	fmt.Fprintln(&b, "## Goal")
	fmt.Fprintln(&b, p.Goal)

	if len(p.SuccessCriteria) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Success criteria (you're done when)")
		for _, s := range p.SuccessCriteria {
			fmt.Fprintln(&b, "- "+s)
		}
	}

	locked, tentative := splitDecisions(p.Decisions)
	if len(locked) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Locked decisions (do not revisit)")
		for _, d := range locked {
			fmt.Fprintf(&b, "- %s\n  Rationale: %s\n", d.What, d.Why)
			if d.Consequences != "" {
				fmt.Fprintf(&b, "  Consequences: %s\n", d.Consequences)
			}
		}
	}

	if failed := filterAttempts(p.Attempts, "failed", "partial"); len(failed) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Prior attempts (do not retry without new information)")
		for _, a := range failed {
			fmt.Fprintf(&b, "- [%s] %s\n", a.Outcome, a.Tried)
			if a.Learned != "" {
				fmt.Fprintf(&b, "  Learned: %s\n", a.Learned)
			}
			if a.Confidence != "" {
				fmt.Fprintf(&b, "  Confidence this outcome is final: %s\n", a.Confidence)
			}
		}
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Your first action")
	fmt.Fprintln(&b, p.NextStep)

	// ---- MIDDLE (lower-attention background) ----

	if len(p.Constraints) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Constraints")
		for _, c := range p.Constraints {
			fmt.Fprintln(&b, "- "+c)
		}
	}

	if len(p.Methods) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Reusable methods (apply when the trigger matches)")
		for _, m := range p.Methods {
			fmt.Fprintf(&b, "- **%s** — when: %s\n  how: %s\n", m.Name, m.WhenApplicable, m.How)
		}
	}

	if succeeded := filterAttempts(p.Attempts, "succeeded"); len(succeeded) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Attempts that worked")
		for _, a := range succeeded {
			fmt.Fprintf(&b, "- %s\n", a.Tried)
			if a.Learned != "" {
				fmt.Fprintf(&b, "  Learned: %s\n", a.Learned)
			}
		}
	}

	if len(tentative) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Tentative decisions (can be revisited with cause)")
		for _, d := range tentative {
			fmt.Fprintf(&b, "- %s\n  Rationale: %s\n", d.What, d.Why)
			if d.Consequences != "" {
				fmt.Fprintf(&b, "  Consequences: %s\n", d.Consequences)
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

	// ---- END (high-attention re-anchor) ----

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "---")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Reminder")
	fmt.Fprintf(&b, "Your first action is: **%s**\n", p.NextStep)
	if len(locked) > 0 {
		fmt.Fprintln(&b, "Locked decisions above must be respected.")
	}

	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Packet:    %s (spec v%s)\n", p.ID, p.LTMVersion)
	if p.ParentID != "" {
		fmt.Fprintf(&b, "Continues: %s\n", p.ParentID)
	}
	fmt.Fprintf(&b, "Created:   %s\n", p.CreatedAt.UTC().Format(time.RFC3339))
	if p.Provenance != nil {
		parts := []string{}
		if p.Provenance.AuthorHuman != "" {
			parts = append(parts, p.Provenance.AuthorHuman)
		}
		if p.Provenance.AuthorModel != "" {
			parts = append(parts, "via "+p.Provenance.AuthorModel)
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "Author:    %s\n", strings.Join(parts, " "))
		}
	}
	if len(p.Tags) > 0 {
		fmt.Fprintf(&b, "Tags:      %s\n", strings.Join(p.Tags, ", "))
	}
	return b.String()
}

func splitDecisions(ds []packet.Decision) (locked, tentative []packet.Decision) {
	for _, d := range ds {
		if d.Locked {
			locked = append(locked, d)
		} else {
			tentative = append(tentative, d)
		}
	}
	return
}

func filterAttempts(as []packet.Attempt, outcomes ...string) []packet.Attempt {
	set := make(map[string]struct{}, len(outcomes))
	for _, o := range outcomes {
		set[o] = struct{}{}
	}
	var out []packet.Attempt
	for _, a := range as {
		if _, ok := set[a.Outcome]; ok {
			out = append(out, a)
		}
	}
	return out
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
