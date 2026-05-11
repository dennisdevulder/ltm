package cli

import (
	"strings"
	"testing"
)

// setManagedEnv isolates config to a tempdir and points LTM_HOST at the
// managed platform. Returns no values — env is restored by t's cleanup.
func setManagedEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LTM_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LTM_HOST", defaultHubURL)
}

// recordBrowser swaps browserOpener for a recorder. Returns a pointer that
// the caller can read after running the command. Restoration is registered
// via t.Cleanup.
func recordBrowser(t *testing.T) *string {
	t.Helper()
	var got string
	orig := browserOpener
	browserOpener = func(u string) error { got = u; return nil }
	t.Cleanup(func() { browserOpener = orig })
	return &got
}

func TestPlatform_ManagedHost_PrintsAndOpensURL(t *testing.T) {
	setManagedEnv(t)
	opened := recordBrowser(t)

	out, _, err := run(t, nil, "platform")
	if err != nil {
		t.Fatalf("platform: %v", err)
	}
	if !strings.Contains(out, defaultHubURL) {
		t.Errorf("stdout should contain %q, got: %q", defaultHubURL, out)
	}
	if *opened != defaultHubURL {
		t.Errorf("browserOpener called with %q, want %q", *opened, defaultHubURL)
	}
}

func TestPlatform_SelfHosted_Errors(t *testing.T) {
	_, srv := setupCLI(t)
	opened := recordBrowser(t)

	_, _, err := run(t, nil, "platform")
	if err == nil {
		t.Fatal("expected error for self-hosted host, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "self-hosted") {
		t.Errorf("error should mention self-hosted, got: %q", msg)
	}
	if !strings.Contains(msg, srv.URL) {
		t.Errorf("error should surface the current host %q, got: %q", srv.URL, msg)
	}
	if *opened != "" {
		t.Errorf("browserOpener must not be called when self-hosted, got %q", *opened)
	}
}

func TestPlatform_NoHost_Errors(t *testing.T) {
	t.Setenv("LTM_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LTM_HOST", "")
	opened := recordBrowser(t)

	_, _, err := run(t, nil, "platform")
	if err == nil {
		t.Fatal("expected error when no host configured, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no host configured") {
		t.Errorf("error should mention no host configured, got: %q", msg)
	}
	if !strings.Contains(msg, "ltm auth") {
		t.Errorf("error should suggest 'ltm auth', got: %q", msg)
	}
	if *opened != "" {
		t.Errorf("browserOpener must not be called with no host, got %q", *opened)
	}
}

func TestPlatform_DashboardAlias(t *testing.T) {
	setManagedEnv(t)
	opened := recordBrowser(t)

	out, _, err := run(t, nil, "dashboard")
	if err != nil {
		t.Fatalf("dashboard alias: %v", err)
	}
	if !strings.Contains(out, defaultHubURL) {
		t.Errorf("stdout should contain %q, got: %q", defaultHubURL, out)
	}
	if *opened != defaultHubURL {
		t.Errorf("alias should drive same RunE; browserOpener got %q, want %q", *opened, defaultHubURL)
	}
}

func TestMCP_Platform_ManagedHost_ReturnsURL(t *testing.T) {
	setManagedEnv(t)

	resp := callTool(t, "platform", map[string]any{})
	text, isError := toolText(t, resp)
	if isError {
		t.Fatalf("expected success, got error result: %q", text)
	}
	if text != defaultHubURL {
		t.Errorf("tool returned %q, want %q", text, defaultHubURL)
	}
}

func TestMCP_Platform_SelfHosted_IsError(t *testing.T) {
	_, srv := setupCLI(t)

	resp := callTool(t, "platform", map[string]any{})
	text, isError := toolText(t, resp)
	if !isError {
		t.Fatalf("expected isError=true for self-hosted, got success: %q", text)
	}
	if !strings.Contains(text, "self-hosted") {
		t.Errorf("error text should mention self-hosted, got: %q", text)
	}
	if !strings.Contains(text, srv.URL) {
		t.Errorf("error text should surface current host %q, got: %q", srv.URL, text)
	}
}
