package jobs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const autoTestHelp = "Automatically discover validation from the working files: make test, a package.json test script, Go tests, Cargo tests, or Python tests. Set up dependencies and meaningful tests yourself. For another build system, provide a Makefile test target. If the coding sandbox cannot install dependencies, include reproducible dependency setup in that test target; the runner executes validation independently. Keep generated dependencies and build outputs out of Git. Missing tests are a fixable validation failure; do not ask the user to configure a test profile."

var makeTestTarget = regexp.MustCompile(`(?m)^test\s*::?[^=]`)

// validationCommand resolves only a fixed set of commands. Repository contents
// choose the build system, but are never interpolated into a shell command.
func validationCommand(dir, policy string) (string, error) {
	if policy != "auto" {
		return policy, nil
	}
	read := func(name string) []byte {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return nil
		}
		data, _ := os.ReadFile(path)
		return data
	}
	for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
		if makeTestTarget.Match(read(name)) {
			return "make test", nil
		}
	}
	if data := read("package.json"); len(data) > 0 {
		var pkg struct {
			Scripts        map[string]string `json:"scripts"`
			PackageManager string            `json:"packageManager"`
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			return "", errors.New("automatic validation: fix the invalid package.json")
		}
		if strings.TrimSpace(pkg.Scripts["test"]) == "" {
			return "", errors.New("automatic validation: add a meaningful package.json test script, or a Makefile test target covering the application")
		}
		manager, _, _ := strings.Cut(pkg.PackageManager, "@")
		if manager == "" {
			for _, lock := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}} {
				if len(read(lock.file)) > 0 {
					manager = lock.manager
					break
				}
			}
		}
		switch manager {
		case "pnpm":
			return "pnpm test", nil
		case "yarn":
			return "yarn test", nil
		case "bun":
			return "bun run test", nil
		case "", "npm":
			return "npm test", nil
		default:
			return "", errors.New("automatic validation: unsupported package manager; provide a Makefile test target")
		}
	}
	if len(read("go.mod")) > 0 {
		return "go test ./...", nil
	}
	if len(read("Cargo.toml")) > 0 {
		return "cargo test", nil
	}
	for _, name := range []string{"pyproject.toml", "requirements.txt", "requirements-dev.txt", "setup.cfg", "pytest.ini"} {
		if strings.Contains(string(read(name)), "pytest") {
			return "python3 -m pytest", nil
		}
	}
	for _, pattern := range []string{"tests/test*.py", "test*.py"} {
		files, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, file := range files {
			name, _ := filepath.Rel(dir, file)
			if strings.Contains(string(read(name)), "unittest") {
				if strings.HasPrefix(pattern, "tests/") {
					return "python3 -m unittest discover -s tests -v", nil
				}
				return "python3 -m unittest discover -v", nil
			}
		}
	}
	return "", errors.New("automatic validation: no test command found. Add meaningful tests and a Makefile test target, package.json test script, Go module, Cargo package, or Python test suite; then continue")
}
