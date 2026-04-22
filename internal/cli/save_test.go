package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSave_FromFile(t *testing.T) {
	api, _ := setupCLI(t)
	f := filepath.Join(t.TempDir(), "packet.json")
	if err := os.WriteFile(f, samplePacket(sampleID), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, nil, "save", f)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.Contains(out, sampleID) {
		t.Errorf("expected save to echo packet id, got: %q", out)
	}
	if _, ok := api.packets[sampleID]; !ok {
		t.Error("packet not stored on fake api")
	}
}

func TestSave_FromStdin(t *testing.T) {
	setupCLI(t)
	out, _, err := run(t, bytes.NewReader(samplePacket(sampleID)), "save", "-")
	if err != nil {
		t.Fatalf("save -: %v", err)
	}
	if !strings.Contains(out, sampleID) {
		t.Errorf("expected id on stdout, got: %q", out)
	}
}

// TestSave_BlocksUnredactedSecret is belt-and-braces over the push variant:
// if someone ever wires save around the shared helper, the redaction gate
// must still fire. A packet with a secret must not reach the server.
func TestSave_BlocksUnredactedSecret(t *testing.T) {
	api, _ := setupCLI(t)
	f := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(f, samplePacketWithSecret(), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := run(t, nil, "save", f)
	if err == nil {
		t.Fatal("expected redaction failure")
	}
	if !strings.Contains(stderr, "redactable") {
		t.Errorf("expected redaction warning on stderr, got: %q", stderr)
	}
	if !strings.Contains(err.Error(), "redaction failed") {
		t.Errorf("expected 'redaction failed' error, got: %v", err)
	}
	if _, ok := api.packets[sampleID]; ok {
		t.Error("blocked packet should not reach the server")
	}
}
