package packet

import (
	"regexp"
	"strings"
	"testing"
)

func TestRedactionIssue_String_ShortFound(t *testing.T) {
	// A short Found value renders verbatim — no truncation marker.
	ri := RedactionIssue{Kind: "goal/aws-key", Found: "AKIAEXAMPLE"}
	got := ri.String()
	want := "goal/aws-key: AKIAEXAMPLE"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestRedactionIssue_String_TruncatesLongFound(t *testing.T) {
	// Found strings over 32 chars get cut at 32 and suffixed with an ellipsis
	// so error output stays readable and doesn't blurt long secrets into logs.
	long := strings.Repeat("x", 50)
	ri := RedactionIssue{Kind: "goal/jwt", Found: long}
	got := ri.String()
	if !strings.HasPrefix(got, "goal/jwt: ") {
		t.Errorf("String() should start with kind prefix, got %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("String() should end in ellipsis for long Found, got %q", got)
	}
	// body between "kind: " and the trailing "…" should be 32 chars.
	body := strings.TrimPrefix(got, "goal/jwt: ")
	body = strings.TrimSuffix(body, "…")
	if len(body) != 32 {
		t.Errorf("truncated body len=%d, want 32 (full string: %q)", len(body), got)
	}
}

func TestRedactionIssue_String_ExactlyAtBoundary(t *testing.T) {
	// 32 chars — right at the cutoff — must NOT be truncated.
	body := strings.Repeat("a", 32)
	ri := RedactionIssue{Kind: "k", Found: body}
	got := ri.String()
	if strings.Contains(got, "…") {
		t.Errorf("32-char Found should not be truncated, got %q", got)
	}
}

func TestRandomToken_ShapeAndUniqueness(t *testing.T) {
	// RandomToken returns a 48-char alphanumeric string. The guarantee that
	// matters for auth is uniqueness per call and no unexpected characters —
	// a typo in the generator that emitted fewer chars or non-alnum would
	// silently weaken every server token.
	alnum := regexp.MustCompile(`^[0-9A-Za-z]+$`)
	seen := make(map[string]struct{}, 500)
	for i := 0; i < 500; i++ {
		tok := RandomToken()
		if len(tok) != 48 {
			t.Fatalf("RandomToken len=%d, want 48 (got %q)", len(tok), tok)
		}
		if !alnum.MatchString(tok) {
			t.Fatalf("RandomToken emitted non-alphanumeric chars: %q", tok)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("RandomToken collided after %d iterations: %q", i, tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestRandomToken_LengthIsExact(t *testing.T) {
	// randomToken takes a byte count. Calling with 0 should still succeed
	// (empty string), and with a small count should match that count exactly.
	if got := randomToken(0); got != "" {
		t.Errorf("randomToken(0) = %q, want empty", got)
	}
	if got := randomToken(10); len(got) != 10 {
		t.Errorf("randomToken(10) len=%d, want 10", len(got))
	}
	if got := randomToken(100); len(got) != 100 {
		t.Errorf("randomToken(100) len=%d, want 100", len(got))
	}
}
