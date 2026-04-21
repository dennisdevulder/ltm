package packet

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	validID      = "01JABCDEF0123456789ABCDEFG"
	validCreated = "2026-04-21T12:00:00Z"
)

func minimalPacketJSON(t *testing.T, constraint string) string {
	t.Helper()
	escaped, err := json.Marshal(constraint)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return `{"ltm_version":"0.1","id":"` + validID +
		`","created_at":"` + validCreated +
		`","goal":"g","next_step":"n","constraints":[` + string(escaped) + `]}`
}

func redactConstraint(t *testing.T, payload string) []RedactionIssue {
	t.Helper()
	raw := minimalPacketJSON(t, payload)
	p, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse failed for payload %q: %v", payload, err)
	}
	return Redact(p)
}

func TestRedact_DetectsSecrets(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantSuffix string // expected suffix on the RedactionIssue Kind
	}{
		// Unix absolute paths
		{"unix-users", "look at /Users/alice/secret", "/abs-path"},
		{"unix-home", "see /home/bob/.env file", "/abs-path"},
		{"unix-etc", "config in /etc/passwd matters", "/abs-path"},
		{"unix-opt", "binary under /opt/app/data/store", "/abs-path"},

		// Windows paths — backslash (regression case for the bug this fixes)
		{"win-users-backslash", `path C:\Users\thijs\secret.txt`, "/abs-path"},
		{"win-data-backslash", `D:\data\file`, "/abs-path"},
		{"win-lower-backslash", `c:\temp\x\y`, "/abs-path"},

		// Windows paths — forward-slash (Git Bash, many log formats)
		{"win-users-forward", "see C:/Users/thijs/Desktop", "/abs-path"},
		{"win-repo-forward", "D:/repo/src/main.go", "/abs-path"},

		// AWS keys — permanent
		{"aws-akia", "AKIAIOSFODNN7EXAMPLE", "/aws-key"},
		// AWS keys — STS / role / user / etc.
		{"aws-asia", "key ASIAIOSFODNN7EXAMPLE here", "/aws-key"},
		{"aws-aroa", "role AROAI44QH8DHBEXAMPLE", "/aws-key"},

		// GitHub tokens
		{"gh-pat", "token ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD", "/gh-token"},

		// JWT
		{"jwt", "auth eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "/jwt"},

		// Private key headers
		{"pk-rsa", "header -----BEGIN RSA PRIVATE KEY----- below", "/private-key"},
		{"pk-openssh", "-----BEGIN OPENSSH PRIVATE KEY-----", "/private-key"},
		{"pk-plain", "-----BEGIN PRIVATE KEY-----", "/private-key"},

		// Google API keys
		{"google-api", "key AIzaSyBvE7C9_abcdefghijklmnopqrstuvwxyz done", "/google-api-key"},

		// Slack tokens
		{"slack-bot", "token xoxb-1234567890-abcdefghijkl here", "/slack-token"},
		{"slack-user", "xoxp-1234567890-abcdefghijklmnop", "/slack-token"},

		// Stripe keys — strings are split to avoid tripping GitHub push protection
		// on the test file itself. Runtime value is identical to a real prefix.
		{"stripe-sk-live", "stripe " + "sk" + "_live_" + "abcdefghijklmnopqrstuvwx", "/stripe-key"},
		{"stripe-sk-test", "sk" + "_test_" + "abcdefghijklmnopqrst", "/stripe-key"},
		{"stripe-rk-live", "rk" + "_live_" + "abcdefghijklmnopqrstuvwx", "/stripe-key"},
		{"stripe-whsec", "wh" + "sec_" + "abcdefghijklmnopqrstuvwxyz012345", "/stripe-key"},

		// SSH public keys
		{"ssh-ed25519", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBvpRYKjHU test@host", "/ssh-public-key"},
		{"ssh-rsa", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7 user@laptop", "/ssh-public-key"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := redactConstraint(t, tc.payload)
			if len(issues) == 0 {
				t.Fatalf("expected a redaction issue for %q, got none", tc.payload)
			}
			found := false
			for _, issue := range issues {
				if strings.HasSuffix(issue.Kind, tc.wantSuffix) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected issue kind ending in %q for payload %q, got issues: %+v",
					tc.wantSuffix, tc.payload, issues)
			}
		})
	}
}

func TestRedact_AllowsCleanContent(t *testing.T) {
	// 35 chars after ghp_ — one short of the 36-char minimum; must NOT match.
	shortGhToken := "token ghp_" + strings.Repeat("a", 35)
	// 31 chars after whsec_ — one short of the 32-char minimum; must NOT match.
	shortWhsec := "wh" + "sec_" + strings.Repeat("a", 31)

	cases := []string{
		"build the feature",
		"run tests and lint",
		"./src/foo.go",
		"../lib/util.go",
		"v1.2.3",
		"go1.21.5",
		"pk_live_abcdef123456789012345678", // Stripe PUBLISHABLE key — not sensitive
		"commit abc1234",                   // short hex — shouldn't match anything
		"https://example.com",
		"project-C:later", // colon after letter but no path separator
		"see issue #42",
		shortGhToken, // gh-token below length threshold
		shortWhsec,   // whsec_ below length threshold
	}

	for _, payload := range cases {
		t.Run(payload, func(t *testing.T) {
			issues := redactConstraint(t, payload)
			if len(issues) != 0 {
				t.Errorf("expected no redaction issues for clean content %q, got: %+v",
					payload, issues)
			}
		})
	}
}

func TestRedact_ReportsAllMatchingPatterns(t *testing.T) {
	// Single string that triggers three distinct patterns — all should be reported.
	payload := "path /Users/x/y and AKIAIOSFODNN7EXAMPLE and ghp_" +
		strings.Repeat("a", 40)
	issues := redactConstraint(t, payload)
	got := map[string]bool{}
	for _, issue := range issues {
		got[issue.Kind] = true
	}
	for _, want := range []string{
		"constraints[0]/abs-path",
		"constraints[0]/aws-key",
		"constraints[0]/gh-token",
	} {
		if !got[want] {
			t.Errorf("expected issue kind %q, got issues: %+v", want, issues)
		}
	}
}

func TestRedact_ScansAllFields(t *testing.T) {
	// A payload that triggers exactly one pattern — unix abs-path — used across fields.
	secret := "path /Users/alice/secret"

	p := &Packet{
		LTMVersion: "0.1",
		ID:         validID,
		Goal:       "goal with " + secret,
		NextStep:   "next " + secret,
		Constraints: []string{
			"constraint " + secret,
		},
		Decisions: []Decision{
			{What: "what " + secret, Why: "why " + secret},
		},
		Attempts: []Attempt{
			{Tried: "tried " + secret, Outcome: "succeeded", Learned: "learned " + secret},
		},
		OpenQuestions: []string{"question " + secret},
	}

	issues := Redact(p)
	wantFields := []string{
		"goal/abs-path",
		"next_step/abs-path",
		"constraints[0]/abs-path",
		"decisions[0].what/abs-path",
		"decisions[0].why/abs-path",
		"attempts[0].tried/abs-path",
		"attempts[0].learned/abs-path",
		"open_questions[0]/abs-path",
	}

	got := map[string]bool{}
	for _, issue := range issues {
		got[issue.Kind] = true
	}
	for _, want := range wantFields {
		if !got[want] {
			t.Errorf("expected redaction issue on field %q, issues were: %+v", want, issues)
		}
	}
}

func TestValidate_Valid(t *testing.T) {
	raw := minimalPacketJSON(t, "just a plain constraint")
	if err := Validate([]byte(raw)); err != nil {
		t.Fatalf("expected valid packet to pass Validate, got: %v", err)
	}
}

func TestValidate_SchemaBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantError string // substring expected in error message
	}{
		{
			name:      "id too short",
			raw:       `{"ltm_version":"0.1","id":"01J","created_at":"` + validCreated + `","goal":"g","next_step":"n"}`,
			wantError: "minLength",
		},
		{
			name:      "missing ltm_version",
			raw:       `{"id":"` + validID + `","created_at":"` + validCreated + `","goal":"g","next_step":"n"}`,
			wantError: "missing required field 'ltm_version'",
		},
		{
			name:      "empty goal",
			raw:       `{"ltm_version":"0.1","id":"` + validID + `","created_at":"` + validCreated + `","goal":"","next_step":"n"}`,
			wantError: "minLength",
		},
		{
			name:      "invalid json",
			raw:       `{"ltm_version": "0.1", "id":`,
			wantError: "invalid json",
		},
		{
			name:      "id too long",
			raw:       `{"ltm_version":"0.1","id":"` + strings.Repeat("a", 65) + `","created_at":"` + validCreated + `","goal":"g","next_step":"n"}`,
			wantError: "maxLength",
		},
		{
			name:      "ltm_version unsupported",
			raw:       `{"ltm_version":"v0.1","id":"` + validID + `","created_at":"` + validCreated + `","goal":"g","next_step":"n"}`,
			wantError: "unsupported ltm_version",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]byte(tc.raw))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantError)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("expected error to contain %q, got: %v", tc.wantError, err)
			}
		})
	}
}

func TestValidate_SizeLimit(t *testing.T) {
	base := `{"ltm_version":"0.1","id":"` + validID +
		`","created_at":"` + validCreated +
		`","goal":"g","next_step":"n"}`

	// Pad between `{` and the rest with spaces — valid JSON, inflates byte count.
	build := func(total int) string {
		pad := total - len(base)
		if pad < 0 {
			t.Fatalf("base already %d bytes, cannot build %d-byte packet", len(base), total)
		}
		return "{" + strings.Repeat(" ", pad) + base[1:]
	}

	t.Run("exactly at limit passes", func(t *testing.T) {
		raw := build(MaxPacketBytes)
		if len(raw) != MaxPacketBytes {
			t.Fatalf("setup error: raw is %d bytes, want %d", len(raw), MaxPacketBytes)
		}
		if err := Validate([]byte(raw)); err != nil {
			t.Errorf("expected packet exactly at limit to pass, got: %v", err)
		}
	})

	t.Run("one byte over limit fails", func(t *testing.T) {
		raw := build(MaxPacketBytes + 1)
		err := Validate([]byte(raw))
		if err == nil {
			t.Fatalf("expected error for oversized packet, got nil")
		}
		if !strings.Contains(err.Error(), "max is") {
			t.Errorf("expected error about max size, got: %v", err)
		}
	})
}

func TestEncode_Roundtrip(t *testing.T) {
	raw := minimalPacketJSON(t, "roundtrip payload")
	p1, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("first parse failed: %v", err)
	}

	encoded, err := p1.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	p2, err := Parse(encoded)
	if err != nil {
		t.Fatalf("re-parse of encoded bytes failed: %v\nencoded: %s", err, encoded)
	}

	if p1.ID != p2.ID || p1.Goal != p2.Goal || p1.NextStep != p2.NextStep ||
		p1.LTMVersion != p2.LTMVersion || !p1.CreatedAt.Equal(p2.CreatedAt) {
		t.Errorf("roundtrip mismatch:\n  before: %+v\n  after:  %+v", p1, p2)
	}
	if len(p1.Constraints) != len(p2.Constraints) {
		t.Errorf("constraints length mismatch: before=%d after=%d",
			len(p1.Constraints), len(p2.Constraints))
	}
}

func TestNewID_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := NewID()
		if len(id) != 26 {
			t.Fatalf("NewID() returned id of length %d, want 26: %q", len(id), id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID() returned duplicate after %d iterations: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

// ---- v0.2 schema coverage ----

func TestSupportedVersions(t *testing.T) {
	got := SupportedVersions()
	want := []string{"0.1", "0.2"}
	if len(got) != len(want) {
		t.Fatalf("SupportedVersions()=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SupportedVersions()[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestValidate_V02_Accepts_All_New_Fields(t *testing.T) {
	raw := `{
  "ltm_version": "0.2",
  "id": "` + validID + `",
  "parent_id": "01JABCDEF9999999999AAAAAAA",
  "created_at": "` + validCreated + `",
  "goal": "g",
  "success_criteria": ["app returns 200 on /v1/healthz"],
  "decisions": [
    {"what":"use ltm-hub-db","why":"accessory host","consequences":"locks us to same-box PG","locked":true}
  ],
  "methods": [
    {"name":"refresh-ghcr-login","when_applicable":"push denied","how":"bin/kamal registry login"}
  ],
  "attempts": [
    {"tried":"fine-grained PAT","outcome":"failed","learned":"first-push needs classic PAT","confidence":"high"}
  ],
  "next_step": "n"
}`
	if err := Validate([]byte(raw)); err != nil {
		t.Fatalf("expected v0.2 packet with all new fields to validate, got: %v", err)
	}
	p, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.ParentID == "" {
		t.Error("ParentID not unmarshaled")
	}
	if len(p.SuccessCriteria) != 1 {
		t.Error("SuccessCriteria not unmarshaled")
	}
	if len(p.Methods) != 1 || p.Methods[0].Name != "refresh-ghcr-login" {
		t.Error("Methods not unmarshaled correctly")
	}
	if len(p.Decisions) != 1 || p.Decisions[0].Consequences == "" {
		t.Error("Decision.Consequences not unmarshaled")
	}
	if len(p.Attempts) != 1 || p.Attempts[0].Confidence != "high" {
		t.Error("Attempt.Confidence not unmarshaled")
	}
}

func TestValidate_V01_Rejects_V02_Fields(t *testing.T) {
	// A v0.1 packet carrying v0.2-only fields must fail because v0.1 schema
	// has additionalProperties:false. This protects older servers.
	raw := `{"ltm_version":"0.1","id":"` + validID +
		`","created_at":"` + validCreated +
		`","goal":"g","next_step":"n","parent_id":"01JABCDEF9999999999AAAAAAA"}`
	err := Validate([]byte(raw))
	if err == nil {
		t.Fatal("expected v0.1 schema to reject parent_id, got nil")
	}
	if !strings.Contains(err.Error(), "additional") {
		t.Errorf("expected 'additional properties' error, got: %v", err)
	}
}

func TestNew_ProducesCurrentVersion(t *testing.T) {
	p := New()
	if p.LTMVersion != "0.2" {
		t.Errorf("New() LTMVersion=%q, want 0.2", p.LTMVersion)
	}
}
