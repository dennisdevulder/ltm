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

type Packet struct {
	LTMVersion    string      `json:"ltm_version"`
	ID            string      `json:"id"`
	CreatedAt     time.Time   `json:"created_at"`
	Project       *Project    `json:"project,omitempty"`
	Goal          string      `json:"goal"`
	Constraints   []string    `json:"constraints,omitempty"`
	Decisions     []Decision  `json:"decisions,omitempty"`
	Attempts      []Attempt   `json:"attempts,omitempty"`
	OpenQuestions []string    `json:"open_questions,omitempty"`
	NextStep      string      `json:"next_step"`
	Tags          []string    `json:"tags,omitempty"`
	Provenance    *Provenance `json:"provenance,omitempty"`
}

type Project struct {
	Name string `json:"name,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

type Decision struct {
	What   string `json:"what"`
	Why    string `json:"why"`
	Locked bool   `json:"locked,omitempty"`
}

type Attempt struct {
	Tried   string `json:"tried"`
	Outcome string `json:"outcome"`
	Learned string `json:"learned,omitempty"`
}

type Provenance struct {
	AuthorModel string `json:"author_model,omitempty"`
	AuthorHuman string `json:"author_human,omitempty"`
	SourceHash  string `json:"source_hash,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

var validator *jsonschema.Schema

func init() {
	c := jsonschema.NewCompiler()
	if err := c.AddResource(ltmschema.CoreMemoryV01URL, mustParseJSON(ltmschema.CoreMemoryV01)); err != nil {
		panic(fmt.Errorf("load schema: %w", err))
	}
	s, err := c.Compile(ltmschema.CoreMemoryV01URL)
	if err != nil {
		panic(fmt.Errorf("compile schema: %w", err))
	}
	validator = s
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

// Validate runs the JSON Schema check against raw bytes.
func Validate(raw []byte) error {
	if len(raw) > MaxPacketBytes {
		return fmt.Errorf("packet is %d bytes, max is %d", len(raw), MaxPacketBytes)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid json: %w", err)
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

var (
	absPathUnix = regexp.MustCompile(`(?m)(?:^|[\s"'=:])(/(?:Users|home|opt|var|etc|root)/[A-Za-z0-9._/\-]+)`)
	absPathWin  = regexp.MustCompile(`[A-Za-z]:\\\\[A-Za-z0-9 _.\\\-]+`)
	awsKey      = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	ghToken     = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)
	jwtLike     = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	privateKey  = regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |)PRIVATE KEY-----`)
)

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
		if absPathUnix.MatchString(s) {
			issues = append(issues, RedactionIssue{Kind: field + "/abs-path", Found: absPathUnix.FindString(s)})
		}
		if absPathWin.MatchString(s) {
			issues = append(issues, RedactionIssue{Kind: field + "/abs-path", Found: absPathWin.FindString(s)})
		}
		if awsKey.MatchString(s) {
			issues = append(issues, RedactionIssue{Kind: field + "/aws-key", Found: awsKey.FindString(s)})
		}
		if ghToken.MatchString(s) {
			issues = append(issues, RedactionIssue{Kind: field + "/gh-token", Found: ghToken.FindString(s)})
		}
		if jwtLike.MatchString(s) {
			issues = append(issues, RedactionIssue{Kind: field + "/jwt", Found: jwtLike.FindString(s)})
		}
		if privateKey.MatchString(s) {
			issues = append(issues, RedactionIssue{Kind: field + "/private-key", Found: "-----BEGIN …"})
		}
	}
	scan("goal", p.Goal)
	scan("next_step", p.NextStep)
	for i, c := range p.Constraints {
		scan(fmt.Sprintf("constraints[%d]", i), c)
	}
	for i, d := range p.Decisions {
		scan(fmt.Sprintf("decisions[%d].what", i), d.What)
		scan(fmt.Sprintf("decisions[%d].why", i), d.Why)
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

// New returns a Packet with ID, CreatedAt, and LTMVersion pre-filled.
// Callers are responsible for every other field.
func New() *Packet {
	return &Packet{
		LTMVersion: "0.1",
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
