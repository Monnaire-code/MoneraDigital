package releasecontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const StageRef = "refs/heads/stage"

type Mode string

const (
	ModeMigrationOnly      Mode = "migration-only"
	ModeWorkersOffCurrent  Mode = "workers-off-current"
	ModeServerDark         Mode = "server-dark"
	ModeWorkersOnInstalled Mode = "workers-on-installed"
	ModeStandard           Mode = "standard"
)

type Action string

const (
	ActionInstallMigrate     Action = "install-migrate"
	ActionMigrate            Action = "migrate"
	ActionVerifyInstalledSHA Action = "verify-installed-sha"
	ActionWorkersOff         Action = "workers-off"
	ActionRequireWorkersOff  Action = "require-workers-off"
	ActionInstallServer      Action = "install-server"
	ActionWriteManifest      Action = "write-manifest"
	ActionWorkersOn          Action = "workers-on"
	ActionRestart            Action = "restart"
	ActionHealth             Action = "health"
)

type Plan struct {
	Mode    Mode     `json:"mode"`
	Actions []Action `json:"actions"`
}

var plans = map[Mode][]Action{
	ModeMigrationOnly:      {ActionInstallMigrate, ActionMigrate},
	ModeWorkersOffCurrent:  {ActionVerifyInstalledSHA, ActionWorkersOff, ActionRestart, ActionHealth},
	ModeServerDark:         {ActionRequireWorkersOff, ActionInstallServer, ActionWriteManifest, ActionRestart, ActionHealth},
	ModeWorkersOnInstalled: {ActionVerifyInstalledSHA, ActionWorkersOn, ActionRestart, ActionHealth},
	ModeStandard:           {ActionInstallMigrate, ActionMigrate, ActionInstallServer, ActionWriteManifest, ActionRestart, ActionHealth},
}

func ParseMode(value string) (Mode, error) {
	mode := Mode(strings.TrimSpace(value))
	if _, ok := plans[mode]; !ok {
		return "", fmt.Errorf("unsupported release mode %q", value)
	}
	return mode, nil
}

func PlanForMode(mode Mode) (Plan, error) {
	actions, ok := plans[mode]
	if !ok {
		return Plan{}, fmt.Errorf("unsupported release mode %q", mode)
	}
	return Plan{Mode: mode, Actions: append([]Action(nil), actions...)}, nil
}

var (
	fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
	runID   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	ceiling = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

func ValidateFullSHA(value string) error {
	if !fullSHA.MatchString(value) {
		return errors.New("artifact SHA must be exactly 40 lowercase hexadecimal characters")
	}
	return nil
}

type ControlInput struct {
	EventName         string
	Ref               string
	Mode              Mode
	ArtifactSHA       string
	RunID             string
	ControlLock       string
	MigrationCeiling  string
	StageHead         string
	ArtifactReachable bool
}

type ControlOutput struct {
	Mode             Mode   `json:"mode"`
	ArtifactSHA      string `json:"artifact_sha"`
	RunID            string `json:"run_id,omitempty"`
	MigrationCeiling string `json:"migration_ceiling,omitempty"`
	ControlToken     string `json:"control_token,omitempty"`
	BaselineDigest   string `json:"baseline_digest,omitempty"`
}

type ControlToken struct {
	Raw            string `json:"raw"`
	RunID          string `json:"run_id"`
	BaselineDigest string `json:"baseline_digest"`
}

func ParseControlToken(value string) (ControlToken, error) {
	parts := strings.Split(value, "@")
	if len(parts) != 2 || !runID.MatchString(parts[0]) || !baselineDigest.MatchString(parts[1]) {
		return ControlToken{}, errors.New("repository control token must be run_id@64hex")
	}
	return ControlToken{Raw: value, RunID: parts[0], BaselineDigest: parts[1]}, nil
}

func ValidateControl(input ControlInput) (ControlOutput, error) {
	if input.Ref != StageRef {
		return ControlOutput{}, fmt.Errorf("release ref must be %s", StageRef)
	}
	if err := ValidateFullSHA(input.ArtifactSHA); err != nil {
		return ControlOutput{}, err
	}
	if _, err := PlanForMode(input.Mode); err != nil {
		return ControlOutput{}, err
	}

	switch input.EventName {
	case "push":
		if strings.TrimSpace(input.RunID) != "" {
			return ControlOutput{}, errors.New("push releases cannot use a cutover run id")
		}
		if input.Mode != ModeStandard {
			return ControlOutput{}, errors.New("push releases must use standard mode")
		}
		if strings.TrimSpace(input.ControlLock) != "" {
			return ControlOutput{}, errors.New("stage push is blocked while the cutover lock is set")
		}
	case "workflow_dispatch":
		if !runID.MatchString(input.RunID) {
			return ControlOutput{}, errors.New("manual run id is invalid")
		}
		controlToken, err := ParseControlToken(input.ControlLock)
		if err != nil || controlToken.RunID != input.RunID {
			return ControlOutput{}, errors.New("manual run id does not match the repository control lock")
		}
		if err := ValidateFullSHA(input.StageHead); err != nil {
			return ControlOutput{}, errors.New("manual release requires the full stage HEAD")
		}
		if !input.ArtifactReachable {
			return ControlOutput{}, errors.New("manual artifact is not reachable from stage")
		}
		if strings.HasSuffix(input.RunID, "_FINAL") && input.Mode == ModeStandard && input.ArtifactSHA != input.StageHead {
			return ControlOutput{}, errors.New("FINAL standard must deploy exact stage HEAD")
		}
		if input.Mode == ModeMigrationOnly && !ceiling.MatchString(input.MigrationCeiling) {
			return ControlOutput{}, errors.New("migration-only requires an expected migration ceiling")
		}
	case "":
		return ControlOutput{}, errors.New("event name is required")
	default:
		return ControlOutput{}, fmt.Errorf("unsupported release event %q", input.EventName)
	}

	output := ControlOutput{Mode: input.Mode, ArtifactSHA: input.ArtifactSHA, RunID: input.RunID, MigrationCeiling: input.MigrationCeiling}
	if input.EventName == "workflow_dispatch" {
		controlToken, _ := ParseControlToken(input.ControlLock)
		output.ControlToken = controlToken.Raw
		output.BaselineDigest = controlToken.BaselineDigest
	}
	return output, nil
}

type Manifest struct {
	ServerSHA string `json:"server_sha"`
}

func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := ValidateFullSHA(manifest.ServerSHA); err != nil {
		return Manifest{}, fmt.Errorf("invalid release manifest: %w", err)
	}
	return manifest, nil
}

func WriteManifest(path string, manifest Manifest) error {
	if err := ValidateFullSHA(manifest.ServerSHA); err != nil {
		return err
	}
	data := []byte(fmt.Sprintf("{\"server_sha\":%q}\n", manifest.ServerSHA))
	tmpPath := filepath.Join(filepath.Dir(path), ".release-manifest.tmp")
	defer os.Remove(tmpPath)
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
