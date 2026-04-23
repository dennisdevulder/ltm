package packet

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	ltmschema "github.com/dennisdevulder/ltm/schema"
)

const MaxPacketBytes = 32 * 1024

// Packet is the in-memory representation of a Core Memory Packet, covering the
// union of fields defined by v0.1 and v0.2. Fields unique to v0.2 use
// omitempty and are absent from v0.1 packets, so round-tripping a v0.1 packet
// through this struct produces byte-identical v0.1 JSON.
type Packet struct {
	LTMVersion      string      `json:"ltm_version"`
	ID              string      `json:"id"`
	ParentID        string      `json:"parent_id,omitempty"` // v0.2
	CreatedAt       time.Time   `json:"created_at"`
	Project         *Project    `json:"project,omitempty"`
	Goal            string      `json:"goal"`
	SuccessCriteria []string    `json:"success_criteria,omitempty"` // v0.2
	Constraints     []string    `json:"constraints,omitempty"`
	Decisions       []Decision  `json:"decisions,omitempty"`
	Methods         []Method    `json:"methods,omitempty"` // v0.2
	Attempts        []Attempt   `json:"attempts,omitempty"`
	OpenQuestions   []string    `json:"open_questions,omitempty"`
	NextStep        string      `json:"next_step"`
	Tags            []string    `json:"tags,omitempty"`
	Provenance      *Provenance `json:"provenance,omitempty"`
}

type Project struct {
	Name string `json:"name,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

type Decision struct {
	What         string `json:"what"`
	Why          string `json:"why"`
	Consequences string `json:"consequences,omitempty"` // v0.2
	Locked       bool   `json:"locked,omitempty"`
}

// Method captures reusable procedural knowledge (v0.2).
// A receiving agent should treat 'how' as an authoritative recipe for the
// situation described by 'when_applicable'.
type Method struct {
	Name           string `json:"name"`
	WhenApplicable string `json:"when_applicable"`
	How            string `json:"how"`
}

type Attempt struct {
	Tried      string `json:"tried"`
	Outcome    string `json:"outcome"`
	Learned    string `json:"learned,omitempty"`
	Confidence string `json:"confidence,omitempty"` // v0.2: "low" | "medium" | "high"
}

type Provenance struct {
	AuthorModel string `json:"author_model,omitempty"`
	AuthorHuman string `json:"author_human,omitempty"`
	SourceHash  string `json:"source_hash,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

// ---- schema registry ----

// validators maps an ltm_version string (e.g. "0.1", "0.2") to a compiled
// JSON Schema. At validation time we peek at the packet's ltm_version and
// route to the matching validator so v0.1 packets stay strictly v0.1.
var validators = map[string]*jsonschema.Schema{}

func init() {
	for _, entry := range []struct {
		version string
		url     string
		data    []byte
	}{
		{"0.1", ltmschema.CoreMemoryV01URL, ltmschema.CoreMemoryV01},
		{"0.2", ltmschema.CoreMemoryV02URL, ltmschema.CoreMemoryV02},
	} {
		c := jsonschema.NewCompiler()
		if err := c.AddResource(entry.url, mustParseJSON(entry.data)); err != nil {
			panic(fmt.Errorf("load %s schema: %w", entry.version, err))
		}
		s, err := c.Compile(entry.url)
		if err != nil {
			panic(fmt.Errorf("compile %s schema: %w", entry.version, err))
		}
		validators[entry.version] = s
	}
}

func mustParseJSON(b []byte) any {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		panic(err)
	}
	return v
}

// NewID returns a fresh ULID as a string.
func NewID() string {
	return ulid.Make().String()
}

// SupportedVersions returns the sorted list of ltm_version strings this
// package can validate.
func SupportedVersions() []string {
	out := make([]string, 0, len(validators))
	for v := range validators {
		out = append(out, v)
	}
	// tiny hand-sort for stability (2–3 entries)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[i] > out[j] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Validate runs the JSON Schema check against raw bytes. The schema is
// chosen by the packet's declared ltm_version; unknown versions are
// rejected rather than silently accepted.
func Validate(raw []byte) error {
	if len(raw) > MaxPacketBytes {
		return fmt.Errorf("packet is %d bytes, max is %d", len(raw), MaxPacketBytes)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("packet must be a JSON object")
	}
	ver, _ := m["ltm_version"].(string)
	if ver == "" {
		return fmt.Errorf("missing required field 'ltm_version'")
	}
	validator, ok := validators[ver]
	if !ok {
		return fmt.Errorf("unsupported ltm_version %q (supported: %s)", ver, strings.Join(SupportedVersions(), ", "))
	}
	if err := validator.Validate(v); err != nil {
		return fmt.Errorf("schema violation: %w", err)
	}
	return nil
}

// Parse unmarshals validated bytes into a Packet.
func Parse(raw []byte) (*Packet, error) {
	if err := Validate(raw); err != nil {
		return nil, err
	}
	var p Packet
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Encode produces canonical JSON for storage/wire.
func (p *Packet) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ---- redaction ----
//
// Implements the pre-flight specified in SPEC.md § Redaction; read that
// section for the normative rules. Implementation notes:
//
//   - Patterns are RE2 regexes held in a flat slice. Each entry carries a
//     kind label and an optional mask used in the reported Found field.
//   - Redact walks the parsed *Packet struct field by field and records
//     at most one RedactionIssue per (field, pattern). The serialized
//     JSON is never scanned.
//   - The override (--allow-unredacted in the CLI; allow_unredacted=true
//     via MCP) is handled in the push/save code paths, not here; Redact
//     is the pure check.
//   - The reference server does not re-run Redact on receipt; redaction
//     is a writer obligation.
//
// Adding a new pattern: append to redactionPatterns. Adding a new
// scannable field on Packet: also add a scan(...) call in Redact — the
// scanner does not reflect over fields automatically.

type redactionPattern struct {
	kind string
	re   *regexp.Regexp
	// mask, when non-empty, replaces the matched text in the reported Found
	// field (used for private-key headers where echoing the match is noise).
	mask string
}

var redactionPatterns = []redactionPattern{
	{kind: "abs-path", re: regexp.MustCompile(`\B/(?:Users|home|opt|var|etc|root)/[A-Za-z0-9._/\-]+`)},
	{kind: "abs-path", re: regexp.MustCompile(`\b[A-Za-z]:[\\/](?:[A-Za-z0-9 _.\-]+[\\/]?)+`)},
	{kind: "aws-key", re: regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ABIA|ACCA)[0-9A-Z]{16}\b`)},
	{kind: "gh-token", re: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{kind: "jwt", re: regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)},
	{kind: "private-key", re: regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |)PRIVATE KEY-----`), mask: "-----BEGIN …"},
	{kind: "google-api-key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`)},
	{kind: "slack-token", re: regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z\-]{10,}\b`)},
	// Stripe: sk_/rk_ accept 20+ alnum (short restricted keys exist). whsec_
	// (webhook signing secret) is always ~35 base64 chars in practice, so we
	// keep that branch stricter to reduce false positives on short tokens.
	{kind: "stripe-key", re: regexp.MustCompile(`\b(?:(?:sk|rk)_(?:live|test)_[0-9A-Za-z]{20,}|whsec_[0-9A-Za-z]{32,})\b`)},
	{kind: "ssh-public-key", re: regexp.MustCompile(`\bssh-(?:rsa|ed25519|dss|ecdsa)\s+AAAA[0-9A-Za-z+/=]{20,}`)},
}

type RedactionIssue struct {
	Kind  string
	Found string
}

func (ri RedactionIssue) String() string {
	f := ri.Found
	if len(f) > 32 {
		f = f[:32] + "…"
	}
	return fmt.Sprintf("%s: %s", ri.Kind, f)
}

// Redact scans a packet's visible text fields for path/secret patterns.
// Returns a list of issues; empty slice means the packet is safe to travel.
func Redact(p *Packet) []RedactionIssue {
	var issues []RedactionIssue
	scan := func(field, s string) {
		for _, pat := range redactionPatterns {
			if m := pat.re.FindString(s); m != "" {
				found := m
				if pat.mask != "" {
					found = pat.mask
				}
				issues = append(issues, RedactionIssue{Kind: field + "/" + pat.kind, Found: found})
			}
		}
	}
	scan("goal", p.Goal)
	scan("next_step", p.NextStep)
	for i, c := range p.SuccessCriteria {
		scan(fmt.Sprintf("success_criteria[%d]", i), c)
	}
	for i, c := range p.Constraints {
		scan(fmt.Sprintf("constraints[%d]", i), c)
	}
	for i, d := range p.Decisions {
		scan(fmt.Sprintf("decisions[%d].what", i), d.What)
		scan(fmt.Sprintf("decisions[%d].why", i), d.Why)
		scan(fmt.Sprintf("decisions[%d].consequences", i), d.Consequences)
	}
	for i, m := range p.Methods {
		scan(fmt.Sprintf("methods[%d].when_applicable", i), m.WhenApplicable)
		scan(fmt.Sprintf("methods[%d].how", i), m.How)
	}
	for i, a := range p.Attempts {
		scan(fmt.Sprintf("attempts[%d].tried", i), a.Tried)
		scan(fmt.Sprintf("attempts[%d].learned", i), a.Learned)
	}
	for i, q := range p.OpenQuestions {
		scan(fmt.Sprintf("open_questions[%d]", i), q)
	}
	return issues
}

// ---- helpers ----

// New returns a Packet with ID, CreatedAt, and LTMVersion pre-filled for the
// current schema version. Callers are responsible for every other field.
func New() *Packet {
	return &Packet{
		LTMVersion: ltmschema.Current,
		ID:         NewID(),
		CreatedAt:  time.Now().UTC(),
	}
}

func randomToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	var sb strings.Builder
	const chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for _, x := range b {
		sb.WriteByte(chars[int(x)%len(chars)])
	}
	return sb.String()
}

// RandomToken returns a 48-char alphanumeric token for auth.
func RandomToken() string { return randomToken(48) }
