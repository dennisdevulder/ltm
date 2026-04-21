package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// HashToken returns the hex-encoded sha256 of a token.
// Servers store hashes, never the token itself.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CredentialsPath returns ~/.config/ltm/credentials.
func CredentialsPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials"), nil
}

func configDir() (string, error) {
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

// SaveToken writes token to the credentials file with 0600 perms.
func SaveToken(token string) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "credentials")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	return nil
}

// LoadToken reads the credentials file, if present.
func LoadToken() (string, error) {
	path, err := CredentialsPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := string(b)
	// trim trailing whitespace
	for len(tok) > 0 && (tok[len(tok)-1] == '\n' || tok[len(tok)-1] == '\r' || tok[len(tok)-1] == ' ') {
		tok = tok[:len(tok)-1]
	}
	if tok == "" {
		return "", fmt.Errorf("empty credentials file")
	}
	return tok, nil
}
