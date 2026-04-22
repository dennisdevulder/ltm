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

// minimalV02 is a helper for v0.2 packets. Extras is an optional JSON fragment
// (already wrapped in its own key:value pairs) spliced just before the closing
// brace so tests can add one or two v0.2-specific fields without re-typing the
// boilerplate every time.
func minimalV02(extras string) string {
	base := `{"ltm_version":"0.2","id":"` + validID +
		`","created_at":"` + validCreated +
		`","goal":"g","next_step":"n"`
	if extras != "" {
		base += "," + extras
	}
	return base + "}"
}

// ---- unsupported ltm_version ----

func TestValidate_UnsupportedVersion_ListsSupportedVersions(t *testing.T) {
	// The error message is load-bearing — downstream tooling surfaces it to
	// end users who need to know which versions their CLI understands.
	raw := `{"ltm_version":"0.3","id":"` + validID +
		`","created_at":"` + validCreated +
		`","goal":"g","next_step":"n"}`
	err := Validate([]byte(raw))
	if err == nil {
		t.Fatal("expected error for unsupported ltm_version, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unsupported ltm_version") {
		t.Errorf("expected 'unsupported ltm_version' in error, got: %v", err)
	}
	if !strings.Contains(msg, `"0.3"`) {
		t.Errorf("expected error to echo back the bad version, got: %v", err)
	}
	for _, v := range SupportedVersions() {
		if !strings.Contains(msg, v) {
			t.Errorf("expected error to list supported version %q, got: %v", v, err)
		}
	}
}

func TestValidate_UnsupportedVersion_Variants(t *testing.T) {
	// Each of these must be rejected at the version-routing step, before the
	// packet ever reaches a JSON Schema. If we forget to reject any of these
	// a client could silently pick whichever schema it happens to match last.
	cases := []struct {
		name    string
		version string
	}{
		{"future minor", "0.3"},
		{"future major", "1.0"},
		{"v-prefix", "v0.2"},
		{"with-patch", "0.2.0"},
		{"empty string", ""},
		{"whitespace", " 0.2"},
		{"zero", "0"},
		{"non-numeric", "beta"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"ltm_version":"` + tc.version + `","id":"` + validID +
				`","created_at":"` + validCreated +
				`","goal":"g","next_step":"n"}`
			err := Validate([]byte(raw))
			if err == nil {
				t.Fatalf("expected error for ltm_version=%q, got nil", tc.version)
			}
			// Empty string hits the "missing required field" branch; every
			// other form should hit the "unsupported" branch.
			if tc.version == "" {
				if !strings.Contains(err.Error(), "missing required field 'ltm_version'") {
					t.Errorf("empty version should trigger missing-field error, got: %v", err)
				}
				return
			}
			if !strings.Contains(err.Error(), "unsupported ltm_version") {
				t.Errorf("expected 'unsupported ltm_version' error for %q, got: %v", tc.version, err)
			}
		})
	}
}

func TestValidate_LTMVersionNotString(t *testing.T) {
	// An int, null, or array in the version slot must not crash the
	// type assertion — it falls through to the missing-field error.
	cases := []string{
		`{"ltm_version":2,"id":"` + validID + `","created_at":"` + validCreated + `","goal":"g","next_step":"n"}`,
		`{"ltm_version":null,"id":"` + validID + `","created_at":"` + validCreated + `","goal":"g","next_step":"n"}`,
		`{"ltm_version":["0.2"],"id":"` + validID + `","created_at":"` + validCreated + `","goal":"g","next_step":"n"}`,
	}
	for _, raw := range cases {
		err := Validate([]byte(raw))
		if err == nil {
			t.Errorf("expected error for non-string ltm_version in %s, got nil", raw)
			continue
		}
		if !strings.Contains(err.Error(), "missing required field 'ltm_version'") {
			t.Errorf("expected missing-field error for non-string version, got: %v", err)
		}
	}
}

// ---- methods[].name rejection ----

func TestValidate_MethodsName_Rejection(t *testing.T) {
	// Schema pattern: ^[a-z0-9][a-z0-9-]{0,127}$
	// The name field is the identity hook agents use to look up a recipe.
	// Anything that isn't lowercase-kebab-or-digit has to be rejected cleanly
	// so recipes can't ship with names that are hard to match, search, or log.
	cases := []struct {
		name   string
		method string // full methods[0] JSON fragment
	}{
		{
			"uppercase-name",
			`{"name":"RefreshLogin","when_applicable":"push denied","how":"retry"}`,
		},
		{
			"underscore-in-name",
			`{"name":"refresh_login","when_applicable":"push denied","how":"retry"}`,
		},
		{
			"space-in-name",
			`{"name":"refresh login","when_applicable":"push denied","how":"retry"}`,
		},
		{
			"leading-hyphen",
			`{"name":"-refresh","when_applicable":"push denied","how":"retry"}`,
		},
		{
			"leading-dot",
			`{"name":".refresh","when_applicable":"push denied","how":"retry"}`,
		},
		{
			"empty-name",
			`{"name":"","when_applicable":"push denied","how":"retry"}`,
		},
		{
			"unicode-name",
			`{"name":"refresh-login-é","when_applicable":"push denied","how":"retry"}`,
		},
		{
			"too-long-name",
			`{"name":"` + strings.Repeat("a", 129) + `","when_applicable":"push denied","how":"retry"}`,
		},
		{
			"slash-in-name",
			`{"name":"refresh/login","when_applicable":"push denied","how":"retry"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := minimalV02(`"methods":[` + tc.method + `]`)
			err := Validate([]byte(raw))
			if err == nil {
				t.Fatalf("expected schema violation for method %s, got nil", tc.method)
			}
			if !strings.Contains(err.Error(), "schema violation") {
				t.Errorf("expected 'schema violation' in error, got: %v", err)
			}
		})
	}
}

func TestValidate_MethodsName_Accepted(t *testing.T) {
	// Counterpart to the rejection table — these must pass, otherwise the
	// regex is too strict to be useful for real recipe names.
	cases := []string{
		"refresh",
		"refresh-login",
		"r",
		"0start-with-digit",
		"step-1-of-2",
		strings.Repeat("a", 128), // at maxLength
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			raw := minimalV02(`"methods":[{"name":"` + name +
				`","when_applicable":"when","how":"how"}]`)
			if err := Validate([]byte(raw)); err != nil {
				t.Errorf("expected %q to be accepted, got: %v", name, err)
			}
		})
	}
}

func TestValidate_Methods_RequiredFields(t *testing.T) {
	// Every method needs all three: name, when_applicable, how. A method with
	// only a name is useless to a receiving agent — it'd know what the recipe
	// is called but not when or how to apply it.
	cases := []struct {
		name    string
		method  string
		missing string
	}{
		{"missing-name", `{"when_applicable":"w","how":"h"}`, "name"},
		{"missing-when", `{"name":"r","how":"h"}`, "when_applicable"},
		{"missing-how", `{"name":"r","when_applicable":"w"}`, "how"},
		{"empty-object", `{}`, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := minimalV02(`"methods":[` + tc.method + `]`)
			err := Validate([]byte(raw))
			if err == nil {
				t.Fatalf("expected error for method missing %s, got nil", tc.missing)
			}
			if !strings.Contains(err.Error(), "schema violation") {
				t.Errorf("expected schema violation, got: %v", err)
			}
		})
	}
}

func TestValidate_Methods_AdditionalPropertiesRejected(t *testing.T) {
	// If we ever add a new method subfield in v0.3, it must not silently
	// land in v0.2 packets today — schema has additionalProperties:false.
	raw := minimalV02(`"methods":[{"name":"r","when_applicable":"w","how":"h","extra":"oops"}]`)
	err := Validate([]byte(raw))
	if err == nil {
		t.Fatal("expected extra method field to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "additional") {
		t.Errorf("expected 'additional properties' error, got: %v", err)
	}
}

func TestValidate_Methods_MaxItems(t *testing.T) {
	// maxItems:32 — at 32 should pass, at 33 should fail.
	method := `{"name":"r","when_applicable":"w","how":"h"}`
	pass := strings.TrimSuffix(strings.Repeat(method+",", 32), ",")
	if err := Validate([]byte(minimalV02(`"methods":[` + pass + `]`))); err != nil {
		t.Errorf("32 methods should pass, got: %v", err)
	}
	fail := strings.TrimSuffix(strings.Repeat(method+",", 33), ",")
	if err := Validate([]byte(minimalV02(`"methods":[` + fail + `]`))); err == nil {
		t.Error("33 methods should fail, got nil")
	}
}

// ---- attempts[].confidence ----

func TestValidate_AttemptConfidence_EnumRejection(t *testing.T) {
	// Enum is low | medium | high — anything else is a spec violation, not a
	// hint the validator should be clever about. A typoed "med" must fail.
	cases := []string{"medium-high", "HIGH", "med", "0.9", "", "none"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			raw := minimalV02(`"attempts":[{"tried":"t","outcome":"failed","confidence":"` + c + `"}]`)
			err := Validate([]byte(raw))
			if err == nil {
				t.Fatalf("expected schema violation for confidence=%q, got nil", c)
			}
			if !strings.Contains(err.Error(), "schema violation") {
				t.Errorf("expected schema violation, got: %v", err)
			}
		})
	}
}

func TestValidate_AttemptOutcome_EnumRejection(t *testing.T) {
	// Outcome existed in v0.1 but the enum is load-bearing for the v0.2
	// resume-block rendering logic (filterAttempts routes by outcome string).
	// Regressions here would quietly bucket attempts into the wrong section.
	raw := minimalV02(`"attempts":[{"tried":"t","outcome":"maybe"}]`)
	if err := Validate([]byte(raw)); err == nil {
		t.Fatal("expected schema violation for bogus outcome, got nil")
	}
}

// ---- v0.2 field shape ----

func TestValidate_ParentID_Length(t *testing.T) {
	cases := []struct {
		name     string
		parentID string
		wantPass bool
	}{
		{"too-short", "abc", false},
		{"min-length", strings.Repeat("a", 10), true},
		{"typical-ulid", "01JABCDEF9999999999AAAAAAA", true},
		{"max-length", strings.Repeat("a", 64), true},
		{"too-long", strings.Repeat("a", 65), false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := minimalV02(`"parent_id":"` + tc.parentID + `"`)
			err := Validate([]byte(raw))
			if tc.wantPass && err != nil {
				t.Errorf("expected parent_id=%q to pass, got: %v", tc.parentID, err)
			}
			if !tc.wantPass && err == nil {
				t.Errorf("expected parent_id=%q to fail, got nil", tc.parentID)
			}
		})
	}
}

func TestValidate_SuccessCriteria_Bounds(t *testing.T) {
	// maxItems:16 — over the cap must fail. A very long item must also fail.
	items := make([]string, 0, 17)
	for i := 0; i < 17; i++ {
		items = append(items, `"crit"`)
	}
	raw := minimalV02(`"success_criteria":[` + strings.Join(items, ",") + `]`)
	if err := Validate([]byte(raw)); err == nil {
		t.Error("expected 17 success_criteria to fail maxItems, got nil")
	}

	long := `"` + strings.Repeat("a", 1025) + `"`
	raw = minimalV02(`"success_criteria":[` + long + `]`)
	if err := Validate([]byte(raw)); err == nil {
		t.Error("expected oversized success_criteria item to fail maxLength, got nil")
	}
}

func TestValidate_Decisions_Consequences_MaxLength(t *testing.T) {
	long := strings.Repeat("x", 1025)
	raw := minimalV02(`"decisions":[{"what":"w","why":"y","consequences":"` + long + `"}]`)
	if err := Validate([]byte(raw)); err == nil {
		t.Error("expected oversized consequences to fail maxLength, got nil")
	}
}

// ---- redaction coverage of v0.2 fields ----

func TestRedact_ScansV02Fields(t *testing.T) {
	// New text-bearing fields must go through the same redaction scan as
	// v0.1 fields — otherwise a secret hidden in a method 'how' slips past
	// the pre-push check that protects users from leaking credentials.
	secret := "/Users/alice/secret"
	p := &Packet{
		LTMVersion:      "0.2",
		ID:              validID,
		Goal:            "g",
		NextStep:        "n",
		SuccessCriteria: []string{"criterion " + secret},
		Decisions: []Decision{
			{What: "w", Why: "y", Consequences: "consequences " + secret},
		},
		Methods: []Method{
			{Name: "m", WhenApplicable: "when " + secret, How: "how " + secret},
		},
	}
	issues := Redact(p)
	wantFields := []string{
		"success_criteria[0]/abs-path",
		"decisions[0].consequences/abs-path",
		"methods[0].when_applicable/abs-path",
		"methods[0].how/abs-path",
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

func TestRedact_SkipsMethodName(t *testing.T) {
	// Method names are constrained to lowercase kebab-case by schema, so they
	// cannot carry paths or tokens. Scanning them would only produce noise —
	// this test pins that we're intentionally not scanning it.
	p := &Packet{
		LTMVersion: "0.2",
		ID:         validID,
		Goal:       "g",
		NextStep:   "n",
		Methods: []Method{
			{Name: "refresh-login", WhenApplicable: "w", How: "h"},
		},
	}
	for _, issue := range Redact(p) {
		if strings.HasPrefix(issue.Kind, "methods[0].name") {
			t.Errorf("method name should not be scanned, but got: %+v", issue)
		}
	}
}
