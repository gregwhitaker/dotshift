package router

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregwhitaker/dotshift/internal/config"
)

// MasterKeyFile is the file, inside the state directory, holding a generated key.
const MasterKeyFile = "master_key"

// MasterKey returns the key clients must send to the proxy. It comes from
// router.master_key_env when set; otherwise dotshift generates one on first use
// and keeps it in stateDir with owner-only permissions. LiteLLM refuses to
// start without a master key.
func MasterKey(cfg *config.Config, stateDir string) (string, error) {
	if env := cfg.Router.MasterKeyEnv; env != "" {
		key := os.Getenv(env)
		if key == "" {
			return "", fmt.Errorf("router.master_key_env is %s, but it is not set", env)
		}
		return key, nil
	}
	path := filepath.Join(stateDir, MasterKeyFile)
	data, err := os.ReadFile(path)
	if err == nil {
		if key := strings.TrimSpace(string(data)); key != "" {
			return key, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// LiteLLM expects master keys to start with "sk-".
	key := "sk-dotshift-" + hex.EncodeToString(buf)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	return key, nil
}
