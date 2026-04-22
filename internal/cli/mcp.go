package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/config"
	"github.com/dennisdevulder/ltm/internal/packet"
)

// mcpServerProtocolVersion is the MCP protocol revision we implement. When a
// client initializes with a different version we echo theirs back if the
// shapes we use (tools/list, tools/call, notifications/initialized) are
// present in their revision — the wire format has been stable across these.
const mcpServerProtocolVersion = "2024-11-05"

func newMcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server over stdio, exposing ltm as tools.",
		Long: `Speaks the Model Context Protocol over stdio so MCP-aware clients
(Claude Code, Cursor, Zed, Claude Desktop, Continue, …) can call ltm verbs
as tools.

Register it with Claude Code:

  claude mcp add ltm -- ltm mcp

Or paste this into your client's MCP config:

  { "ltm": { "command": "ltm", "args": ["mcp"] } }

The server authenticates using the same host + token 'ltm auth' already
stored; if you haven't run 'ltm auth' yet, tools that need the server
(ls, show, resume, push, pull, rm) will return an auth error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCP(os.Stdin, os.Stdout)
		},
	}
}

// ---- JSON-RPC frame types ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// runMCP is the stdio JSON-RPC loop. Each line on stdin is one message; each
// response is one line on stdout. Everything else (logging, warnings) goes to
// stderr so it doesn't corrupt the channel.
func runMCP(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Packets are capped at 32 KB; give plenty of headroom for JSON-RPC envelope.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage(`null`),
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			continue
		}
		// Notifications have no id and expect no response.
		isNotification := len(req.ID) == 0 || string(req.ID) == "null"
		resp := dispatch(&req)
		if isNotification {
			continue
		}
		resp.JSONRPC = "2.0"
		resp.ID = req.ID
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintln(os.Stderr, "ltm mcp: encode error:", err)
			return err
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func dispatch(req *rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return handleInitialize(req)
	case "ping":
		return rpcResponse{Result: struct{}{}}
	case "tools/list":
		return rpcResponse{Result: map[string]any{"tools": toolDefinitions()}}
	case "tools/call":
		return handleToolCall(req)
	}
	// Swallow notifications we don't care about (notifications/initialized,
	// notifications/cancelled, …). For any other unknown request, return
	// method-not-found.
	if strings.HasPrefix(req.Method, "notifications/") {
		return rpcResponse{} // response is discarded for notifications
	}
	return rpcResponse{
		Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method},
	}
}

// ---- initialize ----

func handleInitialize(req *rpcRequest) rpcResponse {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(req.Params, &params)

	version := params.ProtocolVersion
	if version == "" {
		version = mcpServerProtocolVersion
	}
	return rpcResponse{
		Result: map[string]any{
			"protocolVersion": version,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "ltm",
				"version": Version,
			},
		},
	}
}

// ---- tool registry ----

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func toolDefinitions() []toolDef {
	return []toolDef{
		{
			Name:        "ls",
			Description: "List recent packets on the configured ltm server. Returns a table with ID, creation time, and goal.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of packets to return (default 50).",
						"minimum":     1,
						"maximum":     200,
					},
				},
			},
		},
		{
			Name:        "show",
			Description: "Fetch a packet by ID and return a human-readable summary (goal, constraints, decisions, attempts, next step).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Packet ID."},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "pull",
			Description: "Fetch a packet by ID and return the raw JSON document. Use when you need the full v0.2 packet — otherwise prefer 'show' or 'resume'.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Packet ID."},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "resume",
			Description: "Render a prompt-ready resume block for a packet so the current agent can continue prior work. Output is markdown intended to be treated as authoritative context.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Packet ID."},
				},
				"required": []string{"id"},
			},
		},
		{
			Name: "push",
			Description: "Send a Core Memory Packet to the configured ltm server. The packet is schema-validated and scanned for secrets/absolute paths before upload. " +
				"Pass the packet as a JSON object under 'packet'. Use 'ltm example' via the 'example' tool if you need a valid shape reference.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"packet": map[string]any{
						"type":        "object",
						"description": "The full Core Memory Packet JSON (v0.1 or v0.2).",
					},
					"allow_unredacted": map[string]any{
						"type":        "boolean",
						"description": "Skip the redaction pre-flight. Only set true when the caller has already reviewed the content.",
					},
				},
				"required": []string{"packet"},
			},
		},
		{
			Name:        "rm",
			Description: "Delete a packet by ID from the server. Destructive — confirm with the user before calling.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Packet ID to delete."},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "example",
			Description: "Return an embedded sample v0.2 Core Memory Packet. Useful as a shape reference before calling 'push'. No server round-trip.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "whoami",
			Description: "Report the configured ltm host and a short fingerprint of the stored token. Use to verify the server the MCP will hit before pushing.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

// ---- tools/call dispatch ----

func handleToolCall(req *rpcRequest) rpcResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcResponse{Error: &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}}
	}

	var text string
	var err error
	switch params.Name {
	case "ls":
		text, err = toolLs(params.Arguments)
	case "show":
		text, err = toolShow(params.Arguments)
	case "pull":
		text, err = toolPull(params.Arguments)
	case "resume":
		text, err = toolResume(params.Arguments)
	case "push":
		text, err = toolPush(params.Arguments)
	case "rm":
		text, err = toolRm(params.Arguments)
	case "example":
		text = string(embeddedExamplePacket)
	case "whoami":
		text, err = toolWhoami()
	default:
		return rpcResponse{Error: &rpcError{Code: -32602, Message: "unknown tool: " + params.Name}}
	}

	if err != nil {
		return rpcResponse{Result: toolResult("error: "+err.Error(), true)}
	}
	return rpcResponse{Result: toolResult(text, false)}
}

func toolResult(text string, isError bool) map[string]any {
	r := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	}
	if isError {
		r["isError"] = true
	}
	return r
}

// ---- tool handlers ----

func toolLs(raw json.RawMessage) (string, error) {
	var args struct {
		Limit int `json:"limit"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &args)
	}
	switch {
	case args.Limit <= 0:
		args.Limit = 50
	case args.Limit > 200:
		// Match the inputSchema's `maximum` so an LLM that ignores the
		// schema can't ask for an unbounded scan.
		args.Limit = 200
	}
	cl, err := newClient()
	if err != nil {
		return "", err
	}
	_, rows, err := fetchPacketList(cl, args.Limit)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "No packets on server.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-28s  %-25s  %s\n", "ID", "CREATED", "GOAL")
	for _, p := range rows {
		goal := p.Goal
		if len(goal) > 72 {
			goal = goal[:72] + "…"
		}
		fmt.Fprintf(&b, "%-28s  %-25s  %s\n", p.ID, p.CreatedAt, goal)
	}
	return b.String(), nil
}

func toolShow(raw json.RawMessage) (string, error) {
	id, err := argID(raw)
	if err != nil {
		return "", err
	}
	cl, err := newClient()
	if err != nil {
		return "", err
	}
	body, err := fetchPacketBody(cl, id)
	if err != nil {
		return "", err
	}
	return formatPacketSummary(body)
}

func toolPull(raw json.RawMessage) (string, error) {
	id, err := argID(raw)
	if err != nil {
		return "", err
	}
	cl, err := newClient()
	if err != nil {
		return "", err
	}
	body, err := fetchPacketBody(cl, id)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func toolResume(raw json.RawMessage) (string, error) {
	id, err := argID(raw)
	if err != nil {
		return "", err
	}
	cl, err := newClient()
	if err != nil {
		return "", err
	}
	body, err := fetchPacketBody(cl, id)
	if err != nil {
		return "", err
	}
	p, err := packet.Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse packet: %w", err)
	}
	return renderResumeBlock(p), nil
}

func toolPush(raw json.RawMessage) (string, error) {
	var args struct {
		Packet          json.RawMessage `json:"packet"`
		AllowUnredacted bool            `json:"allow_unredacted"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if len(args.Packet) == 0 {
		return "", fmt.Errorf("missing 'packet' argument")
	}

	if err := packet.Validate(args.Packet); err != nil {
		return "", fmt.Errorf("packet rejected: %w", err)
	}
	p, err := packet.Parse(args.Packet)
	if err != nil {
		return "", err
	}
	if !args.AllowUnredacted {
		if issues := packet.Redact(p); len(issues) > 0 {
			var b strings.Builder
			b.WriteString("packet contains redactable content — refusing to push. issues:\n")
			for _, i := range issues {
				fmt.Fprintf(&b, "  - %s\n", i.String())
			}
			b.WriteString("pass allow_unredacted=true to override.")
			return "", errors.New(b.String())
		}
	}

	cl, err := newClient()
	if err != nil {
		return "", err
	}
	resp, err := cl.do("POST", "/v1/packets", args.Packet)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := errFromResponse(resp); err != nil {
		return "", err
	}
	return p.ID, nil
}

func toolRm(raw json.RawMessage) (string, error) {
	id, err := argID(raw)
	if err != nil {
		return "", err
	}
	cl, err := newClient()
	if err != nil {
		return "", err
	}
	resp, err := cl.do("DELETE", "/v1/packets/"+id, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := errFromResponse(resp); err != nil {
		return "", err
	}
	return "deleted " + id, nil
}

func toolWhoami() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	cfg.Resolve()
	tok, _ := auth.LoadToken()
	fp := ""
	if tok != "" {
		fp = auth.HashToken(tok)[:8]
	}
	out := map[string]string{
		"host":        cfg.Host,
		"token_hash8": fp,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

// ---- arg helpers ----

func argID(raw json.RawMessage) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("missing 'id' argument")
	}
	return args.ID, nil
}
