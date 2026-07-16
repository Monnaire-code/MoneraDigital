package releasecontrol

import (
	"fmt"
	"path/filepath"
	"strings"
)

type BootstrapPhase string

const (
	BootstrapD0 BootstrapPhase = "D0"
	BootstrapB0 BootstrapPhase = "B0"
)

const StageWorkflowPath = ".github/workflows/deploy-backend-stage.yml"

var b0ExactPaths = map[string]struct{}{
	StageWorkflowPath:                             {},
	"scripts/deploy-remote.sh":                    {},
	"scripts/db-promote/README.md":                {},
	"cmd/migrate/main.go":                         {},
	"cmd/server/main.go":                          {},
	"internal/db/db.go":                           {},
	"internal/migration/migrator.go":              {},
	"internal/migration/migrator_ceiling_test.go": {},
	"docs/company-fund-stage-release-control.md":  {},
}

var b0Prefixes = []string{
	"cmd/company-fund-release/",
	"internal/buildinfo/",
	"internal/releasecontrol/",
}

func ValidateBootstrapChangedFiles(phase BootstrapPhase, paths []string) error {
	if len(paths) == 0 {
		return errorsForPhase(phase, "changed-file set is empty")
	}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			return errorsForPhase(phase, fmt.Sprintf("invalid changed path %q", path))
		}
		if _, duplicate := seen[clean]; duplicate {
			return errorsForPhase(phase, fmt.Sprintf("duplicate changed path %q", clean))
		}
		seen[clean] = struct{}{}
		if !bootstrapPathAllowed(phase, clean) {
			return errorsForPhase(phase, fmt.Sprintf("path %q is outside the allowlist", clean))
		}
	}
	if _, ok := seen[StageWorkflowPath]; !ok {
		return errorsForPhase(phase, "stage workflow is required")
	}
	return nil
}

func bootstrapPathAllowed(phase BootstrapPhase, path string) bool {
	switch phase {
	case BootstrapD0:
		return path == StageWorkflowPath
	case BootstrapB0:
		if _, ok := b0ExactPaths[path]; ok {
			return true
		}
		for _, prefix := range b0Prefixes {
			if strings.HasPrefix(path, prefix) && len(path) > len(prefix) {
				return true
			}
		}
	}
	return false
}

func errorsForPhase(phase BootstrapPhase, message string) error {
	if phase != BootstrapD0 && phase != BootstrapB0 {
		return fmt.Errorf("unsupported bootstrap phase %q", phase)
	}
	return fmt.Errorf("%s bootstrap: %s", phase, message)
}
