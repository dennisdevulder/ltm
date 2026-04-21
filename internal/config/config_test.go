package config

import (
	"os"
	"path/filepath"
	"testing"
)

// withIsolatedConfigDir points config lookup at a fresh temp dir for the test
// by setting LTM_CONFIG_DIR. It clears any competing env vars so the test is
// hermetic regardless of what the developer's shell has set.
func withIsolatedConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LTM_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LTM_HOST", "")
	t.Setenv("LTM_USER", "")
	t.Setenv("LTM_OUTPUT", "")
	return dir
}

func TestDir_Precedence(t *testing.T) {
	t.Run("LTM_CONFIG_DIR wins", func(t *testing.T) {
		ltmDir := t.TempDir()
		xdgDir := t.TempDir()
		t.Setenv("LTM_CONFIG_DIR", ltmDir)
		t.Setenv("XDG_CONFIG_HOME", xdgDir)
		got, err := Dir()
		if err != nil {
			t.Fatal(err)
		}
		if got != ltmDir {
			t.Errorf("Dir() = %q, want %q", got, ltmDir)
		}
	})

	t.Run("XDG_CONFIG_HOME wins when LTM_CONFIG_DIR unset", func(t *testing.T) {
		xdgDir := t.TempDir()
		t.Setenv("LTM_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", xdgDir)
		got, err := Dir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(xdgDir, "ltm")
		if got != want {
			t.Errorf("Dir() = %q, want %q", got, want)
		}
	})

	t.Run("home fallback when both unset", func(t *testing.T) {
		t.Setenv("LTM_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		got, err := Dir()
		if err != nil {
			t.Fatal(err)
		}
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".config", "ltm")
		if got != want {
			t.Errorf("Dir() = %q, want %q", got, want)
		}
	})
}

func TestKeys_ContainsAllSupportedKeys(t *testing.T) {
	got := Keys()
	want := map[string]bool{
		"host":    true,
		"user":    true,
		"output":  true,
		"lenient": true,
		"editor":  true,
	}
	if len(got) != len(want) {
		t.Fatalf("Keys() len = %d, want %d (got: %v)", len(got), len(want), got)
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("Keys() returned unexpected key %q", k)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("Keys() missing expected key %q", k)
	}
}

func TestLoad_NonExistentReturnsDefaults(t *testing.T) {
	withIsolatedConfigDir(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if c.Output != "human" {
		t.Errorf("default Output = %q, want human", c.Output)
	}
	if c.Host != "" || c.User != "" {
		t.Errorf("expected zero Host/User on fresh load, got %+v", c)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	withIsolatedConfigDir(t)
	in := &Config{
		Host:    "https://example.com",
		User:    "alice",
		Output:  "json",
		Lenient: true,
		Editor:  "vim",
	}
	if err := in.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *out != *in {
		t.Errorf("roundtrip mismatch:\n  in:  %+v\n  out: %+v", *in, *out)
	}

	// File should exist at the expected path with 0600 perms.
	p, _ := Path()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("config file perms = %o, want 600", mode)
	}
}

func TestLoad_BadTOMLReturnsError(t *testing.T) {
	dir := withIsolatedConfigDir(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("this is { not valid = toml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	if err == nil {
		t.Fatal("expected error loading malformed TOML, got nil")
	}
}

func TestResolve_EnvOverridesFile(t *testing.T) {
	withIsolatedConfigDir(t)
	c := &Config{Host: "from-file", User: "file-user", Output: "human"}
	t.Setenv("LTM_HOST", "from-env")
	t.Setenv("LTM_USER", "env-user")
	t.Setenv("LTM_OUTPUT", "json")

	c.Resolve()
	if c.Host != "from-env" {
		t.Errorf("Host = %q, want from-env", c.Host)
	}
	if c.User != "env-user" {
		t.Errorf("User = %q, want env-user", c.User)
	}
	if c.Output != "json" {
		t.Errorf("Output = %q, want json", c.Output)
	}
}

func TestResolve_UnsetEnvLeavesFileValue(t *testing.T) {
	withIsolatedConfigDir(t)
	c := &Config{Host: "kept", User: "kept", Output: "human"}
	c.Resolve()
	if c.Host != "kept" || c.User != "kept" || c.Output != "human" {
		t.Errorf("unset env should not alter config, got: %+v", c)
	}
}

func TestResolve_DoesNotTouchNonOverriddenFields(t *testing.T) {
	// Pins scope: Resolve covers Host/User/Output. Lenient and Editor are file-
	// only on purpose. Any future PR that widens env coverage should update
	// this test consciously.
	withIsolatedConfigDir(t)
	t.Setenv("LTM_HOST", "env-host")
	t.Setenv("LTM_USER", "env-user")
	t.Setenv("LTM_OUTPUT", "json")
	// Variables that should be ignored if anyone adds them by mistake:
	t.Setenv("LTM_LENIENT", "true")
	t.Setenv("LTM_EDITOR", "emacs")

	c := &Config{Lenient: false, Editor: "vim"}
	c.Resolve()

	if c.Lenient {
		t.Error("Lenient changed by Resolve, expected no env override")
	}
	if c.Editor != "vim" {
		t.Errorf("Editor = %q, want vim (Resolve should not touch it)", c.Editor)
	}
}

func TestSetGet_StringField(t *testing.T) {
	c := &Config{}
	if err := c.Set("host", "https://foo"); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get("HOST") // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://foo" {
		t.Errorf("Get(HOST) = %q, want https://foo", got)
	}
}

func TestSetGet_BoolField(t *testing.T) {
	c := &Config{}
	cases := []struct {
		input string
		want  string
	}{
		{"true", "true"},
		{"1", "true"},
		{"yes", "true"},
		{"on", "true"},
		{"false", "false"},
		{"0", "false"},
		{"no", "false"},
		{"off", "false"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if err := c.Set("lenient", tc.input); err != nil {
				t.Fatalf("Set(lenient, %q): %v", tc.input, err)
			}
			got, _ := c.Get("lenient")
			if got != tc.want {
				t.Errorf("Get(lenient) after Set(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSet_InvalidBool(t *testing.T) {
	c := &Config{}
	err := c.Set("lenient", "maybe")
	if err == nil {
		t.Fatal("expected error for invalid bool, got nil")
	}
}

func TestSetGet_UnknownKey(t *testing.T) {
	c := &Config{}
	if _, err := c.Get("nonexistent"); err == nil {
		t.Error("expected Get(unknown) to error, got nil")
	}
	if err := c.Set("nonexistent", "value"); err == nil {
		t.Error("expected Set(unknown) to error, got nil")
	}
}

func TestUnset_ClearsField(t *testing.T) {
	c := &Config{Host: "something"}
	if err := c.Unset("host"); err != nil {
		t.Fatal(err)
	}
	if c.Host != "" {
		t.Errorf("Host after Unset = %q, want empty", c.Host)
	}
}

func TestUnset_ClearsBoolField(t *testing.T) {
	// Regression: previously Unset routed through Set("") which rejected empty
	// strings for bool fields. `ltm config unset lenient` would error in the CLI.
	c := &Config{Lenient: true}
	if err := c.Unset("lenient"); err != nil {
		t.Fatalf("Unset(lenient): %v", err)
	}
	if c.Lenient {
		t.Error("Lenient after Unset = true, want false")
	}
}

func TestUnset_UnknownKey(t *testing.T) {
	c := &Config{}
	if err := c.Unset("nonexistent"); err == nil {
		t.Error("expected Unset(unknown) to error, got nil")
	}
}

func TestAll_ReturnsStableSortedPairs(t *testing.T) {
	c := &Config{Host: "h", User: "u", Output: "json", Editor: "vim", Lenient: true}
	pairs := c.All()
	if len(pairs) != len(Keys()) {
		t.Fatalf("All() len = %d, want %d", len(pairs), len(Keys()))
	}
	// Verify alphabetical order.
	for i := 1; i < len(pairs); i++ {
		if pairs[i-1][0] >= pairs[i][0] {
			t.Errorf("All() not sorted: %q >= %q", pairs[i-1][0], pairs[i][0])
		}
	}
	// Spot-check a couple of values.
	asMap := map[string]string{}
	for _, p := range pairs {
		asMap[p[0]] = p[1]
	}
	if asMap["host"] != "h" {
		t.Errorf("All()[host] = %q, want h", asMap["host"])
	}
	if asMap["lenient"] != "true" {
		t.Errorf("All()[lenient] = %q, want true", asMap["lenient"])
	}
}

func TestLoad_EmptyOutputBackfillsHuman(t *testing.T) {
	dir := withIsolatedConfigDir(t)
	// Write a config where Output is intentionally empty (e.g., user ran `ltm config unset output`).
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`host = "x"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Output != "human" {
		t.Errorf("Output = %q, want human (backfilled)", c.Output)
	}
}
