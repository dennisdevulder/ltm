package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dennisdevulder/ltm/internal/packet"
)

// The embedded example packet is shipped inside the binary and handed to
// first-time users. If it ever stops validating or starts tripping the
// redaction pre-flight, every `ltm example | ltm push -` pipeline breaks
// silently. These tests guard that contract.

func TestEmbeddedExamplePacket_ValidatesAgainstV02Schema(t *testing.T) {
	p, err := packet.Parse(embeddedExamplePacket)
	if err != nil {
		t.Fatalf("embedded example packet failed to parse: %v", err)
	}
	if p.LTMVersion != "0.2" {
		t.Errorf("want ltm_version 0.2, got %q", p.LTMVersion)
	}
	if p.Goal == "" || p.NextStep == "" {
		t.Error("required goal/next_step fields are empty")
	}
}

func TestEmbeddedExamplePacket_PassesRedactionPreflight(t *testing.T) {
	p, err := packet.Parse(embeddedExamplePacket)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	issues := packet.Redact(p)
	if len(issues) > 0 {
		var sb strings.Builder
		for _, i := range issues {
			sb.WriteString("  - " + i.String() + "\n")
		}
		t.Fatalf("example packet trips redaction pre-flight:\n%s", sb.String())
	}
}

func TestExampleCommand_EmitsEmbeddedJSON(t *testing.T) {
	cmd := newExampleCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("example command failed: %v", err)
	}
	if !bytes.Equal(out.Bytes(), embeddedExamplePacket) {
		t.Error("example command output does not match embedded packet bytes")
	}
}

func TestExampleCommand_ResumeFlagRendersBlock(t *testing.T) {
	cmd := newExampleCmd()
	if err := cmd.Flags().Set("resume", "true"); err != nil {
		t.Fatalf("set --resume: %v", err)
	}
	// Use --no-copy so the test doesn't require a working clipboard
	// (CI containers don't have one). The rendering itself is exercised
	// by resume_test.go; here we just assert the command runs cleanly.
	if err := cmd.Flags().Set("no-copy", "true"); err != nil {
		t.Fatalf("set --no-copy: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("example --resume --no-copy failed: %v", err)
	}
}
