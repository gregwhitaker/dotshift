package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/config"
)

func TestRunArgs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	c, err := config.Parse([]byte("providers:\n  openai: {api_key_env: OPENAI_API_KEY}\naliases:\n  smart: [openai/gpt-5]\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(c, "/tmp/x/litellm.yaml", "sk-master")
	args := strings.Join(r.RunArgs(), " ")
	for _, want := range []string{
		"--name dotshift-litellm",
		"--publish 127.0.0.1:4000:4000",
		"--volume /tmp/x/litellm.yaml:/app/config.yaml:ro",
		"--env OPENAI_API_KEY --env LITELLM_MASTER_KEY ",
		"ghcr.io/berriai/litellm:main-stable --config /app/config.yaml --port 4000",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("run args missing %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, "sk-secret") || strings.Contains(args, "sk-master") {
		t.Error("secret value on the docker command line")
	}
	if got := r.BaseURL(); got != "http://127.0.0.1:4000/v1" {
		t.Errorf("BaseURL = %q", got)
	}
}

func TestRunArgsWithUsage(t *testing.T) {
	c, err := config.Parse([]byte("providers:\n  openai: {}\naliases:\n  smart: [openai/gpt-5]\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(c, "x", "k")
	r.SendUsage("http://host.docker.internal:5555/usage", "tok-secret")
	args := strings.Join(r.RunArgs(), " ")
	if !strings.Contains(args, "--env GENERIC_LOGGER_ENDPOINT --env GENERIC_LOGGER_HEADERS ") {
		t.Errorf("usage env missing:\n%s", args)
	}
	if strings.Contains(args, "tok-secret") {
		t.Error("usage token on the docker command line")
	}
}

func TestMissingEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	c, err := config.Parse([]byte("providers:\n  openai: {api_key_env: OPENAI_API_KEY}\naliases:\n  smart: [openai/gpt-5]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := New(c, "x", "k").MissingEnv(); len(got) != 1 || got[0] != "OPENAI_API_KEY" {
		t.Errorf("MissingEnv = %v", got)
	}
}

func TestMasterKeyGeneratedOnceAndPrivate(t *testing.T) {
	dir := t.TempDir()
	c, err := config.Parse([]byte("providers:\n  openai: {}\naliases:\n  smart: [openai/gpt-5]\n"))
	if err != nil {
		t.Fatal(err)
	}
	k1, err := MasterKey(c, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k1, "sk-") {
		t.Errorf("key %q lacks sk- prefix", k1)
	}
	k2, err := MasterKey(c, dir)
	if err != nil || k2 != k1 {
		t.Errorf("second call = %q, %v; want the same key", k2, err)
	}
	info, err := os.Stat(filepath.Join(dir, MasterKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 600", perm)
	}
}

func TestMasterKeyFromEnv(t *testing.T) {
	c, err := config.Parse([]byte("router: {master_key_env: MY_KEY}\nproviders:\n  openai: {}\naliases:\n  smart: [openai/gpt-5]\n"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MY_KEY", "")
	if _, err := MasterKey(c, t.TempDir()); err == nil {
		t.Error("expected an error when the env var is unset")
	}
	t.Setenv("MY_KEY", "sk-from-env")
	if k, err := MasterKey(c, t.TempDir()); err != nil || k != "sk-from-env" {
		t.Errorf("MasterKey = %q, %v", k, err)
	}
}
