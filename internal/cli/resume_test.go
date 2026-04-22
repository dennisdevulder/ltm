package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dennisdevulder/ltm/internal/packet"
)

// fullV02Packet returns a packet exercising every v0.2-specific field so tests
// can assert the rendering logic surfaces each one in the right section of the
// resume block.
func fullV02Packet() *packet.Packet {
	return &packet.Packet{
		LTMVersion: "0.2",
		ID:         "01JABCDEF0123456789ABCDEFG",
		ParentID:   "01JPARENT0000000000AAAAAAA",
		CreatedAt:  time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
		Goal:       "ship the v0.2 schema",
		SuccessCriteria: []string{
			"v0.1 servers still validate v0.1 packets",
			"unsupported versions rejected with a clear error",
		},
		Constraints: []string{"stay backwards compatible with v0.1"},
		Decisions: []Decision(nil),
		Methods: []packet.Method{
			{Name: "refresh-ghcr", WhenApplicable: "push denied", How: "bin/kamal registry login"},
		},
		Attempts: []packet.Attempt{
			{Tried: "silently degrade", Outcome: "failed", Learned: "reject instead", Confidence: "high"},
			{Tried: "route by version", Outcome: "succeeded", Learned: "clean split"},
		},
		OpenQuestions: []string{"should methods inherit from parent?"},
		NextStep:      "merge after review",
	}
}

// Decision is re-exported for brevity in fullV02Packet's literal above.
type Decision = packet.Decision

func TestRenderResumeBlock_IncludesV02Fields(t *testing.T) {
	p := fullV02Packet()
	p.Decisions = []packet.Decision{
		{What: "single-parent DAG", Why: "keep it simple", Consequences: "multi-parent is a v0.3 concern", Locked: true},
		{What: "no method inheritance", Why: "receiver shouldn't walk chain", Locked: false},
	}

	block := renderResumeBlock(p)

	// Every v0.2 field should appear somewhere in the block — absence of any
	// one of these is a rendering regression that would hide context from
	// the agent.
	wantSubstrings := []string{
		"## Success criteria",
		"v0.1 servers still validate v0.1 packets",
		"## Locked decisions",
		"single-parent DAG",
		"Consequences: multi-parent is a v0.3 concern",
		"## Tentative decisions",
		"no method inheritance",
		"## Reusable methods",
		"**refresh-ghcr**",
		"bin/kamal registry login",
		"Confidence this outcome is final: high",
		"Continues: 01JPARENT0000000000AAAAAAA",
		"(spec v0.2)",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(block, want) {
			t.Errorf("expected resume block to contain %q, block was:\n%s", want, block)
		}
	}
}

func TestRenderResumeBlock_OrderingFollowsPositionBias(t *testing.T) {
	// Liu et al. 2023: models attend most to the start and end of a prompt.
	// The rendering contract is that goal, success criteria, locked
	// decisions, failed attempts, and the first-action block sit BEFORE the
	// lower-priority middle sections (constraints, methods, tentative
	// decisions, succeeded attempts, open questions). If someone reorders
	// sections without thinking about this, the test flags it.
	p := fullV02Packet()
	p.Decisions = []packet.Decision{
		{What: "locked one", Why: "y", Locked: true},
		{What: "tentative one", Why: "y", Locked: false},
	}

	block := renderResumeBlock(p)

	order := []string{
		"## Goal",
		"## Success criteria",
		"## Locked decisions",
		"## Prior attempts",
		"## Your first action",
		"## Constraints",
		"## Reusable methods",
		"## Attempts that worked",
		"## Tentative decisions",
		"## Open questions",
		"## Reminder",
	}
	lastIdx := -1
	lastSection := ""
	for _, section := range order {
		idx := strings.Index(block, section)
		if idx == -1 {
			t.Errorf("expected section %q in block, but it was missing", section)
			continue
		}
		if idx < lastIdx {
			t.Errorf("section %q appears before %q (positions %d < %d); ordering regression",
				section, lastSection, idx, lastIdx)
		}
		lastIdx = idx
		lastSection = section
	}
}

func TestRenderResumeBlock_ReminderRepeatsNextStep(t *testing.T) {
	// The reminder-at-end pattern is load-bearing: it re-anchors the agent
	// on its first action at the end of the context window, where models
	// also attend strongly. If the reminder stops including next_step, the
	// rendering degrades silently.
	p := fullV02Packet()
	p.NextStep = "merge after review"

	block := renderResumeBlock(p)
	// next_step must appear at least twice — once in "Your first action"
	// and again in the "Reminder" block at the end.
	if n := strings.Count(block, "merge after review"); n < 2 {
		t.Errorf("expected next_step to appear at least twice (first action + reminder), got %d times", n)
	}
	// And the reminder must come after the first-action section.
	firstIdx := strings.Index(block, "## Your first action")
	remIdx := strings.Index(block, "## Reminder")
	if remIdx <= firstIdx {
		t.Errorf("reminder section should come after first-action section, got first=%d rem=%d",
			firstIdx, remIdx)
	}
}

func TestRenderResumeBlock_OmitsEmptyV02Sections(t *testing.T) {
	// A v0.1-shaped packet (no success criteria, no methods, no parent, no
	// confidence) must render cleanly — no empty headers, no "Continues:"
	// line, no stray "Confidence…" lines.
	p := &packet.Packet{
		LTMVersion: "0.1",
		ID:         "01JABCDEF0123456789ABCDEFG",
		CreatedAt:  time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
		Goal:       "ship",
		NextStep:   "push",
	}
	block := renderResumeBlock(p)
	forbidden := []string{
		"## Success criteria",
		"## Reusable methods",
		"## Locked decisions",
		"## Tentative decisions",
		"## Prior attempts",
		"## Attempts that worked",
		"## Open questions",
		"## Constraints",
		"Continues:",
		"Confidence this outcome is final",
	}
	for _, f := range forbidden {
		if strings.Contains(block, f) {
			t.Errorf("expected empty packet to omit %q, block was:\n%s", f, block)
		}
	}
}

func TestSplitDecisions(t *testing.T) {
	ds := []packet.Decision{
		{What: "a", Locked: true},
		{What: "b", Locked: false},
		{What: "c", Locked: true},
	}
	locked, tentative := splitDecisions(ds)
	if len(locked) != 2 || locked[0].What != "a" || locked[1].What != "c" {
		t.Errorf("locked=%+v, want [a c]", locked)
	}
	if len(tentative) != 1 || tentative[0].What != "b" {
		t.Errorf("tentative=%+v, want [b]", tentative)
	}
}

func TestFilterAttempts(t *testing.T) {
	as := []packet.Attempt{
		{Tried: "x", Outcome: "succeeded"},
		{Tried: "y", Outcome: "failed"},
		{Tried: "z", Outcome: "partial"},
	}
	failed := filterAttempts(as, "failed", "partial")
	if len(failed) != 2 {
		t.Fatalf("expected 2 failed/partial attempts, got %d: %+v", len(failed), failed)
	}
	succeeded := filterAttempts(as, "succeeded")
	if len(succeeded) != 1 || succeeded[0].Tried != "x" {
		t.Errorf("succeeded=%+v, want [x]", succeeded)
	}
	none := filterAttempts(as, "nonexistent")
	if len(none) != 0 {
		t.Errorf("expected no matches for unknown outcome, got %+v", none)
	}
}

func TestShortID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"short", "short"},
		{"01234567", "01234567"},
		{"0123456789", "…23456789"},
		{"01JABCDEF0123456789ABCDEFG", "…9ABCDEFG"},
	}
	for _, tc := range cases {
		if got := shortID(tc.in); got != tc.want {
			t.Errorf("shortID(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCollapseWS(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"a b", "a b"},
		{"a   b", "a b"},
		{"\ta\nb\t", "a b"},
		{"  leading and trailing  ", "leading and trailing"},
	}
	for _, tc := range cases {
		if got := collapseWS(tc.in); got != tc.want {
			t.Errorf("collapseWS(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHumanRelTime_Buckets(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name     string
		ts       time.Time
		contains string
	}{
		{"just-now", now.Add(-10 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "min ago"},
		{"hours", now.Add(-3 * time.Hour), "hr ago"},
		{"days", now.Add(-3 * 24 * time.Hour), "d ago"},
		{"date-fallback", now.Add(-30 * 24 * time.Hour), now.Add(-30 * 24 * time.Hour).Format("2006-01")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := humanRelTime(tc.ts.Format(time.RFC3339))
			if !strings.Contains(got, tc.contains) {
				t.Errorf("humanRelTime(%s)=%q, want to contain %q", tc.ts, got, tc.contains)
			}
		})
	}
	// Malformed input must fall through, not crash.
	if got := humanRelTime("not-a-date"); got != "not-a-date" {
		t.Errorf("humanRelTime on bad input should return input verbatim, got %q", got)
	}
}
