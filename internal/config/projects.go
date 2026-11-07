package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (p Projects) validate() []string {
	var errs []string
	if p.Root != "" && !filepath.IsAbs(p.Root) && !strings.HasPrefix(p.Root, "~/") {
		errs = append(errs, "projects.root must be an absolute path or start with ~/")
	}
	if p.Root != "" && len(p.TestProfiles) == 0 {
		errs = append(errs, "projects.test_profiles needs at least one named test command when projects.root is enabled")
	}
	for name, command := range p.TestProfiles {
		if !aliasPattern.MatchString(name) {
			errs = append(errs, fmt.Sprintf("projects.test_profiles.%s: names must be lowercase letters, digits, - or _", name))
		}
		if strings.TrimSpace(command) == "" {
			errs = append(errs, fmt.Sprintf("projects.test_profiles.%s: test command is required", name))
		}
	}
	return errs
}
