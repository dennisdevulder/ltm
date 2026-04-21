package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the user-editable config at ~/.config/ltm/config.toml.
// Fields map 1:1 to CLI keys (e.g. `ltm config set host <v>` → Host).
type Config struct {
	Host    string `toml:"host,omitempty"`
	User    string `toml:"user,omitempty"`
	Output  string `toml:"output,omitempty"`
	Lenient bool   `toml:"lenient,omitempty"`
	Editor  string `toml:"editor,omitempty"`
}

// valid key names as used on the CLI.
var keyToField = map[string]string{
	"host":    "Host",
	"user":    "User",
	"output":  "Output",
	"lenient": "Lenient",
	"editor":  "Editor",
}

func Keys() []string {
	out := make([]string, 0, len(keyToField))
	for k := range keyToField {
		out = append(out, k)
	}
	return out
}

func Dir() (string, error) {
	if v := os.Getenv("LTM_CONFIG_DIR"); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "ltm"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ltm"), nil
}

func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.toml"), nil
}

func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	c := &Config{Output: "human"}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if c.Output == "" {
		c.Output = "human"
	}
	return c, nil
}

func (c *Config) Save() error {
	d, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	p, err := Path()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// Resolve applies env-var overrides. Precedence: env > file.
func (c *Config) Resolve() {
	if v := os.Getenv("LTM_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("LTM_USER"); v != "" {
		c.User = v
	}
	if v := os.Getenv("LTM_OUTPUT"); v != "" {
		c.Output = v
	}
}

// Get returns a string view of the named key.
func (c *Config) Get(key string) (string, error) {
	field, ok := keyToField[strings.ToLower(key)]
	if !ok {
		return "", fmt.Errorf("unknown config key %q. try one of: %s", key, strings.Join(Keys(), ", "))
	}
	v := reflect.ValueOf(c).Elem().FieldByName(field)
	switch v.Kind() {
	case reflect.String:
		return v.String(), nil
	case reflect.Bool:
		if v.Bool() {
			return "true", nil
		}
		return "false", nil
	default:
		return fmt.Sprintf("%v", v.Interface()), nil
	}
}

// Set assigns a string value to the named key, parsing as needed.
func (c *Config) Set(key, value string) error {
	field, ok := keyToField[strings.ToLower(key)]
	if !ok {
		return fmt.Errorf("unknown config key %q. try one of: %s", key, strings.Join(Keys(), ", "))
	}
	v := reflect.ValueOf(c).Elem().FieldByName(field)
	switch v.Kind() {
	case reflect.String:
		v.SetString(value)
	case reflect.Bool:
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			v.SetBool(true)
		case "false", "0", "no", "off":
			v.SetBool(false)
		default:
			return fmt.Errorf("not a boolean: %q", value)
		}
	default:
		return fmt.Errorf("unsupported field kind for %s", key)
	}
	return nil
}

// Unset zeroes a key.
func (c *Config) Unset(key string) error {
	return c.Set(key, "")
}

// All returns every key=value pair in a stable order.
func (c *Config) All() [][2]string {
	keys := Keys()
	// sort for stability
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		v, _ := c.Get(k)
		out = append(out, [2]string{k, v})
	}
	return out
}
