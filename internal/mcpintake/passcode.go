package mcpintake

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregwhitaker/dotshift/internal/config"
)

// PasscodeFile is the file, inside the state directory, holding a generated passcode.
const PasscodeFile = "mcp_passcode"

// passcodeAlphabet leaves out letters and digits that are easy to misread.
const passcodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// Passcode returns the passcode that approves MCP clients. It comes from
// mcp.passcode_env when set; otherwise dotshift generates one on first use
// and keeps it in stateDir with owner-only permissions.
func Passcode(cfg *config.Config, stateDir string) (string, error) {
	if env := cfg.MCP.PasscodeEnv; env != "" {
		p := os.Getenv(env)
		if len(normalizePasscode(p)) < 12 {
			return "", fmt.Errorf("mcp.passcode_env is %s, but it is not set or shorter than 12 characters", env)
		}
		return p, nil
	}
	path := filepath.Join(stateDir, PasscodeFile)
	data, err := os.ReadFile(path)
	if err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			return p, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	p, err := NewPasscode()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	return p, os.WriteFile(path, []byte(p+"\n"), 0o600)
}

// NewPasscode makes a random passcode like "k7mq2-x9tbd-4hwpe-ra3nc": 20
// characters, about 99 bits.
func NewPasscode() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, c := range buf {
		if i > 0 && i%5 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(passcodeAlphabet[int(c)%len(passcodeAlphabet)])
	}
	return b.String(), nil
}
