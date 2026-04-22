package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// ---- helpers ----

// callDispatch marshals params into a rpcRequest and runs it through dispatch.
// Returns the decoded response so tests can inspect Result/Error.
func callDispatch(t *testing.T, method string, params any) rpcResponse {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	req := &rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  method,
		Params:  raw,
	}
	if params == nil {
		req.Params = nil
	}
	return dispatch(req)
}

// callTool invokes tools/call for the given tool with the given arguments.
func callTool(t *testing.T, name string, args any) rpcResponse {
	t.Helper()
	return callDispatch(t, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

// toolText pulls the single text block out of a tools/call result. Fails the
// test loudly if shape doesn't match — every tool in this package returns
// exactly one text content block.
func toolText(t *testing.T, resp rpcResponse) (text string, isError bool) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %#v", resp.Result)
	}
	if e, ok := m["isError"].(bool); ok && e {
		isError = true
	}
	content, ok := m["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content is not []map[string]any: %#v", m["content"])
	}
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}
	text, _ = content[0]["text"].(string)
	return text, isError
}

// ---- initialize ----

func TestMCP_Initialize_EchoesClientVersion(t *testing.T) {
	resp := callDispatch(t, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
	})
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	if m["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %v, want the client's value echoed", m["protocolVersion"])
	}
	caps, ok := m["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities missing: %#v", m)
	}
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities.tools missing; tools must be advertised")
	}
	info, ok := m["serverInfo"].(map[string]any)
	if !ok || info["name"] != "ltm" {
		t.Errorf("serverInfo.name = %v, want ltm", info["name"])
	}
	if info["version"] != Version {
		t.Errorf("serverInfo.version = %v, want %s", info["version"], Version)
	}
}

func TestMCP_Initialize_DefaultsWhenMissing(t *testing.T) {
	// No protocolVersion from the client → server falls back to its own.
	resp := callDispatch(t, "initialize", map[string]any{})
	m := resp.Result.(map[string]any)
	if m["protocolVersion"] != mcpServerProtocolVersion {
		t.Errorf("protocolVersion = %v, want default %q",
			m["protocolVersion"], mcpServerProtocolVersion)
	}
}

// ---- basic routing ----

func TestMCP_Ping(t *testing.T) {
	resp := callDispatch(t, "ping", nil)
	if resp.Error != nil {
		t.Errorf("ping error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Error("ping result is nil; want {}")
	}
}

func TestMCP_UnknownMethod(t *testing.T) {
	resp := callDispatch(t, "does/not/exist", nil)
	if resp.Error == nil {
		t.Fatal("expected method-not-found error")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code = %d, want -32601", resp.Error.Code)
	}
}

func TestMCP_NotificationsSilenced(t *testing.T) {
	// Anything under notifications/* is swallowed — the dispatcher returns
	// an empty response and the loop drops it rather than emitting a reply.
	resp := callDispatch(t, "notifications/initialized", nil)
	if resp.Error != nil {
		t.Errorf("notifications should not produce an error, got: %+v", resp.Error)
	}
	if resp.Result != nil {
		t.Errorf("notifications should produce no result, got: %#v", resp.Result)
	}
}

// ---- tools/list ----

func TestMCP_ToolsList_ReturnsAllTools(t *testing.T) {
	resp := callDispatch(t, "tools/list", nil)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	tools, ok := m["tools"].([]toolDef)
	if !ok {
		t.Fatalf("tools field wrong type: %#v", m["tools"])
	}
	want := []string{"ls", "show", "pull", "resume", "push", "rm", "example", "whoami"}
	got := make(map[string]bool, len(tools))
	for _, td := range tools {
		got[td.Name] = true
		if td.Description == "" {
			t.Errorf("tool %q has empty description", td.Name)
		}
		if td.InputSchema == nil {
			t.Errorf("tool %q missing inputSchema", td.Name)
		}
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("tools/list missing %q", name)
		}
	}
}

func TestMCP_ToolsList_RequiredArgs(t *testing.T) {
	// show/pull/resume/rm all take a single 'id' and it must be required —
	// otherwise a caller can forget it and get a useless server round-trip.
	resp := callDispatch(t, "tools/list", nil)
	tools := resp.Result.(map[string]any)["tools"].([]toolDef)
	byName := map[string]toolDef{}
	for _, td := range tools {
		byName[td.Name] = td
	}
	for _, name := range []string{"show", "pull", "resume", "rm"} {
		req, ok := byName[name].InputSchema["required"].([]string)
		if !ok || len(req) == 0 || req[0] != "id" {
			t.Errorf("tool %q should require 'id', got required=%v", name, req)
		}
	}
	// push's required list contains 'packet'.
	req, ok := byName["push"].InputSchema["required"].([]string)
	if !ok || len(req) == 0 || req[0] != "packet" {
		t.Errorf("push should require 'packet', got %v", req)
	}
}

// ---- tools/call routing ----

func TestMCP_ToolsCall_UnknownTool(t *testing.T) {
	resp := callTool(t, "nope", map[string]any{})
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code = %d, want -32602", resp.Error.Code)
	}
}

func TestMCP_ToolsCall_BadParamsShape(t *testing.T) {
	// tools/call with params that can't unmarshal into {name, arguments}.
	req := &rpcRequest{
		Method: "tools/call",
		Params: json.RawMessage(`"not-an-object"`),
	}
	resp := dispatch(req)
	if resp.Error == nil {
		t.Fatal("expected invalid-params error")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code = %d, want -32602", resp.Error.Code)
	}
}

// ---- argID helper ----

func TestArgID_Extracts(t *testing.T) {
	got, err := argID(json.RawMessage(`{"id":"abc"}`))
	if err != nil {
		t.Fatalf("argID: %v", err)
	}
	if got != "abc" {
		t.Errorf("id = %q, want abc", got)
	}
}

func TestArgID_MissingID(t *testing.T) {
	_, err := argID(json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "missing 'id'") {
		t.Errorf("expected missing-id error, got: %v", err)
	}
}

func TestArgID_Malformed(t *testing.T) {
	_, err := argID(json.RawMessage(`not-json`))
	if err == nil || !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("expected invalid-arguments error, got: %v", err)
	}
}

// ---- tool: example ----

func TestMCP_Tool_Example_ReturnsEmbeddedPacket(t *testing.T) {
	// example has no server round-trip; it just echoes the embedded JSON.
	// Verify it's valid JSON — that's the contract the example command makes.
	resp := callTool(t, "example", map[string]any{})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("example returned error: %s", text)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("example output not valid JSON: %v\n%s", err, text)
	}
	if parsed["ltm_version"] == nil {
		t.Error("embedded example packet missing ltm_version")
	}
}

// ---- tool: whoami ----

func TestMCP_Tool_Whoami(t *testing.T) {
	dir := withIsolatedCLIState(t)
	seedCreds(t, dir, "abc-token")
	seedConfig(t, dir, `host = "http://example"`+"\n")

	resp := callTool(t, "whoami", map[string]any{})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("whoami returned error: %s", text)
	}
	var parsed struct {
		Host       string `json:"host"`
		TokenHash8 string `json:"token_hash8"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("whoami output not JSON: %v\n%s", err, text)
	}
	if parsed.Host != "http://example" {
		t.Errorf("host = %q, want http://example", parsed.Host)
	}
	if len(parsed.TokenHash8) != 8 {
		t.Errorf("token_hash8 = %q, want 8 chars", parsed.TokenHash8)
	}
}

// ---- tool: ls ----

func TestMCP_Tool_Ls(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	resp := callTool(t, "ls", map[string]any{})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("ls returned error: %s", text)
	}
	if !strings.Contains(text, sampleID) {
		t.Errorf("ls output missing sample id, got:\n%s", text)
	}
	// Default limit of 50 should land on the server.
	if len(api.requests) == 0 || !strings.Contains(api.requests[0].path, "limit=50") {
		t.Errorf("expected default limit=50, saw paths: %#v", pathsOf(api.requests))
	}
}

func TestMCP_Tool_Ls_CustomLimit(t *testing.T) {
	api, _ := setupCLI(t)
	resp := callTool(t, "ls", map[string]any{"limit": 3})
	if _, isErr := toolText(t, resp); isErr {
		t.Fatal("ls should not error")
	}
	if !strings.Contains(api.requests[0].path, "limit=3") {
		t.Errorf("expected limit=3, got %q", api.requests[0].path)
	}
}

func TestMCP_Tool_Ls_ClampsOversizedLimit(t *testing.T) {
	// inputSchema declares maximum=200; a caller that ignores the schema
	// must still be clamped, not silently forwarded to the server.
	api, _ := setupCLI(t)
	resp := callTool(t, "ls", map[string]any{"limit": 9999})
	if _, isErr := toolText(t, resp); isErr {
		t.Fatal("ls should not error on oversized limit")
	}
	if !strings.Contains(api.requests[0].path, "limit=200") {
		t.Errorf("expected clamp to limit=200, got %q", api.requests[0].path)
	}
}

func TestMCP_Tool_Ls_Empty(t *testing.T) {
	setupCLI(t)
	resp := callTool(t, "ls", map[string]any{})
	text, _ := toolText(t, resp)
	if !strings.Contains(text, "No packets") {
		t.Errorf("expected empty-state message, got:\n%s", text)
	}
}

// ---- tool: show ----

func TestMCP_Tool_Show(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	resp := callTool(t, "show", map[string]any{"id": sampleID})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("show returned error: %s", text)
	}
	// formatPacketSummary output signatures.
	for _, want := range []string{sampleID, "Goal", "ship it", "Next step", "merge"} {
		if !strings.Contains(text, want) {
			t.Errorf("show output missing %q, got:\n%s", want, text)
		}
	}
}

func TestMCP_Tool_Show_MissingID(t *testing.T) {
	setupCLI(t)
	resp := callTool(t, "show", map[string]any{})
	text, isErr := toolText(t, resp)
	if !isErr {
		t.Fatal("expected isError=true when id is missing")
	}
	if !strings.Contains(text, "missing 'id'") {
		t.Errorf("expected missing-id message, got: %q", text)
	}
}

// ---- tool: pull ----

func TestMCP_Tool_Pull_ReturnsRawJSON(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	resp := callTool(t, "pull", map[string]any{"id": sampleID})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("pull returned error: %s", text)
	}
	// Raw JSON — not a formatted summary. Should parse and have the id.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("pull output not JSON: %v\n%s", err, text)
	}
	if parsed["id"] != sampleID {
		t.Errorf("id = %v, want %s", parsed["id"], sampleID)
	}
}

func TestMCP_Tool_Pull_NotFound(t *testing.T) {
	setupCLI(t)
	resp := callTool(t, "pull", map[string]any{"id": "01JBADBADBAD000000000000000"})
	text, isErr := toolText(t, resp)
	if !isErr {
		t.Fatalf("expected isError, got text: %s", text)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' in error, got: %q", text)
	}
}

// ---- tool: resume ----

func TestMCP_Tool_Resume(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	resp := callTool(t, "resume", map[string]any{"id": sampleID})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("resume returned error: %s", text)
	}
	for _, want := range []string{"# Resume context", "## Goal", "ship it", "## Your first action", "merge"} {
		if !strings.Contains(text, want) {
			t.Errorf("resume missing %q, got:\n%s", want, text)
		}
	}
}

// ---- tool: push ----

func TestMCP_Tool_Push_Valid(t *testing.T) {
	api, _ := setupCLI(t)

	var packet map[string]any
	_ = json.Unmarshal(samplePacket(sampleID), &packet)

	resp := callTool(t, "push", map[string]any{"packet": packet})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("push returned error: %s", text)
	}
	if strings.TrimSpace(text) != sampleID {
		t.Errorf("push result = %q, want just the id %q", text, sampleID)
	}
	if _, ok := api.packets[sampleID]; !ok {
		t.Error("packet not stored on fake api")
	}
}

func TestMCP_Tool_Push_MissingPacketArg(t *testing.T) {
	setupCLI(t)
	resp := callTool(t, "push", map[string]any{})
	text, isErr := toolText(t, resp)
	if !isErr {
		t.Fatal("expected isError when 'packet' argument is missing")
	}
	if !strings.Contains(text, "missing 'packet'") {
		t.Errorf("expected missing-packet message, got: %q", text)
	}
}

func TestMCP_Tool_Push_InvalidPacket(t *testing.T) {
	setupCLI(t)
	// Missing required 'goal' field.
	bad := map[string]any{
		"ltm_version": "0.2",
		"id":          sampleID,
		"created_at":  "2026-04-21T12:00:00Z",
		"next_step":   "n",
	}
	resp := callTool(t, "push", map[string]any{"packet": bad})
	text, isErr := toolText(t, resp)
	if !isErr {
		t.Fatalf("expected validation failure, got: %s", text)
	}
	if !strings.Contains(text, "rejected") {
		t.Errorf("expected 'rejected' in error, got: %q", text)
	}
}

func TestMCP_Tool_Push_BlocksSecret(t *testing.T) {
	api, _ := setupCLI(t)

	var packet map[string]any
	_ = json.Unmarshal(samplePacketWithSecret(), &packet)

	resp := callTool(t, "push", map[string]any{"packet": packet})
	text, isErr := toolText(t, resp)
	if !isErr {
		t.Fatal("expected redaction to block push")
	}
	if !strings.Contains(text, "redactable") {
		t.Errorf("expected 'redactable' in error, got: %q", text)
	}
	if !strings.Contains(text, "allow_unredacted") {
		t.Errorf("error should mention the override flag, got: %q", text)
	}
	if _, ok := api.packets[sampleID]; ok {
		t.Error("blocked packet should not reach the server")
	}
}

func TestMCP_Tool_Push_AllowUnredactedBypasses(t *testing.T) {
	api, _ := setupCLI(t)

	var packet map[string]any
	_ = json.Unmarshal(samplePacketWithSecret(), &packet)

	resp := callTool(t, "push", map[string]any{
		"packet":           packet,
		"allow_unredacted": true,
	})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("push with allow_unredacted failed: %s", text)
	}
	if _, ok := api.packets[sampleID]; !ok {
		t.Error("packet should have been stored despite secret")
	}
}

// ---- tool: rm ----

func TestMCP_Tool_Rm(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	resp := callTool(t, "rm", map[string]any{"id": sampleID})
	text, isErr := toolText(t, resp)
	if isErr {
		t.Fatalf("rm returned error: %s", text)
	}
	if !strings.Contains(text, "deleted") || !strings.Contains(text, sampleID) {
		t.Errorf("expected 'deleted <id>', got: %q", text)
	}
	if _, ok := api.packets[sampleID]; ok {
		t.Error("packet still on server after rm")
	}
}

func TestMCP_Tool_Rm_NotFound(t *testing.T) {
	setupCLI(t)
	resp := callTool(t, "rm", map[string]any{"id": "01JBADBADBAD000000000000000"})
	_, isErr := toolText(t, resp)
	if !isErr {
		t.Error("expected isError for unknown id")
	}
}

// ---- runMCP (stdio loop) ----

// sendMessages runs the stdio loop against the given inputs and returns the
// decoded response lines. Each element of `msgs` becomes one newline-delimited
// JSON message. Notifications produce no response line.
func runStdio(t *testing.T, msgs []string) []rpcResponse {
	t.Helper()
	in := strings.NewReader(strings.Join(msgs, "\n") + "\n")
	var out bytes.Buffer
	if err := runMCP(in, &out); err != nil {
		t.Fatalf("runMCP: %v", err)
	}
	var responses []rpcResponse
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("response line not JSON: %v\n%s", err, line)
		}
		responses = append(responses, r)
	}
	return responses
}

func TestMCP_Stdio_InitializeRoundTrip(t *testing.T) {
	resps := runStdio(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
	})
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", resps[0].JSONRPC)
	}
	if string(resps[0].ID) != "1" {
		t.Errorf("id = %s, want 1", string(resps[0].ID))
	}
	if resps[0].Error != nil {
		t.Errorf("unexpected error: %+v", resps[0].Error)
	}
}

func TestMCP_Stdio_NotificationProducesNoResponse(t *testing.T) {
	// An initialize followed by notifications/initialized — only the first
	// should get a reply on stdout.
	resps := runStdio(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	})
	if len(resps) != 1 {
		t.Errorf("want exactly 1 response (notifications are silent), got %d: %#v",
			len(resps), resps)
	}
}

func TestMCP_Stdio_ParseError(t *testing.T) {
	var out bytes.Buffer
	if err := runMCP(strings.NewReader("not json at all\n"), &out); err != nil {
		t.Fatalf("runMCP: %v", err)
	}
	var r rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &r); err != nil {
		t.Fatalf("response not JSON: %v\n%s", err, out.String())
	}
	if r.Error == nil {
		t.Fatal("expected error for malformed input")
	}
	if r.Error.Code != -32700 {
		t.Errorf("code = %d, want -32700 (parse error)", r.Error.Code)
	}
	// Parse-error replies carry a null id per JSON-RPC.
	if string(r.ID) != "null" {
		t.Errorf("id = %s, want null", string(r.ID))
	}
}

func TestMCP_Stdio_BlankLinesSkipped(t *testing.T) {
	// Empty/whitespace-only lines between messages must not produce parse
	// errors — some clients pad with blank lines.
	resps := runStdio(t, []string{
		``,
		`   `,
		`{"jsonrpc":"2.0","id":42,"method":"ping"}`,
	})
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if string(resps[0].ID) != "42" {
		t.Errorf("id = %s, want 42", string(resps[0].ID))
	}
}

func TestMCP_Stdio_MultipleMessages(t *testing.T) {
	resps := runStdio(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	})
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	if string(resps[0].ID) != "1" || string(resps[1].ID) != "2" {
		t.Errorf("ids = %s, %s; want 1, 2", resps[0].ID, resps[1].ID)
	}
}

func TestMCP_Stdio_UnknownMethodWireFormat(t *testing.T) {
	// Method-not-found must still be returned with jsonrpc=2.0 and the
	// original id — that's what clients correlate by.
	resps := runStdio(t, []string{
		`{"jsonrpc":"2.0","id":"x-1","method":"wat"}`,
	})
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("want one error response, got %#v", resps)
	}
	if string(resps[0].ID) != `"x-1"` {
		t.Errorf("id = %s, want \"x-1\"", string(resps[0].ID))
	}
	if resps[0].Error.Code != -32601 {
		t.Errorf("code = %d, want -32601", resps[0].Error.Code)
	}
}

// ---- newMcpCmd wiring ----

func TestNewMcpCmd_Registered(t *testing.T) {
	// Double-check that `ltm mcp` exists on the root command so the feature
	// is reachable end-to-end. Everything else in this file tests the
	// handler; this verifies the command-tree wiring.
	root := NewRootCmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "mcp" {
			found = true
			if c.Args == nil {
				t.Errorf("mcp command should set Args to reject extra args")
			}
			break
		}
	}
	if !found {
		t.Error("`mcp` subcommand not registered on root")
	}
}

// ---- misc ----

func pathsOf(reqs []recordedReq) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.path
	}
	return out
}

// Regression guard: tool handlers must carry the configured bearer token.
// Without this, the fake-api tests could pass against a server that wasn't
// actually auth-gated in the test path.
func TestMCP_Tool_SendsAuthHeader(t *testing.T) {
	api, _ := setupCLI(t)
	resp := callTool(t, "ls", map[string]any{})
	if _, isErr := toolText(t, resp); isErr {
		t.Fatal("ls should not error")
	}
	if len(api.requests) == 0 || api.requests[0].auth != "Bearer test-token" {
		t.Errorf("auth header missing or wrong: %#v", api.requests)
	}
}
