package releasecontrol

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var baselineDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ApprovedControlInput struct {
	EventName              string
	Mode                   Mode
	ArtifactSHA            string
	RunID                  string
	PreflightControlToken  string
	RepositoryControlToken string
	EnvironmentLock        string
	EnvironmentStarted     string
	StageHead              string
	ArtifactReachable      bool
}

type CutoverStarted struct {
	RunID          string
	StartedAt      time.Time
	BaselineDigest string
}

func ValidateApprovedControl(input ApprovedControlInput) (CutoverStarted, error) {
	switch input.EventName {
	case "push":
		if input.PreflightControlToken != "" || input.RepositoryControlToken != "" || input.EnvironmentLock != "" ||
			input.EnvironmentStarted != "" {
			return CutoverStarted{}, errors.New("stage push is blocked by active release controls")
		}
		return CutoverStarted{}, nil
	case "workflow_dispatch":
		if input.PreflightControlToken != input.RepositoryControlToken || input.EnvironmentLock != input.RunID {
			return CutoverStarted{}, errors.New("approved release locks do not match the run id")
		}
		controlToken, err := ParseControlToken(input.PreflightControlToken)
		if err != nil || controlToken.RunID != input.RunID {
			return CutoverStarted{}, errors.New("approved repository control token does not match the run id")
		}
		started, err := ParseCutoverStarted(input.EnvironmentStarted)
		if err != nil {
			return CutoverStarted{}, err
		}
		if started.RunID != input.RunID {
			return CutoverStarted{}, errors.New("cutover started run does not match the approved run")
		}
		if started.BaselineDigest != controlToken.BaselineDigest {
			return CutoverStarted{}, errors.New("cutover started baseline digest does not match the trusted control token")
		}
		if err := ValidateFullSHA(input.StageHead); err != nil {
			return CutoverStarted{}, errors.New("approved release requires the full stage HEAD")
		}
		if !input.ArtifactReachable {
			return CutoverStarted{}, errors.New("approved artifact is not reachable from stage")
		}
		if input.Mode == ModeStandard && strings.HasSuffix(input.RunID, "_FINAL") && input.ArtifactSHA != input.StageHead {
			return CutoverStarted{}, errors.New("FINAL standard must deploy exact stage HEAD")
		}
		return started, nil
	default:
		return CutoverStarted{}, fmt.Errorf("unsupported release event %q", input.EventName)
	}
}

func ParseCutoverStarted(value string) (CutoverStarted, error) {
	parts := strings.Split(value, "@")
	if len(parts) != 3 || parts[0] == "" || !baselineDigest.MatchString(parts[2]) {
		return CutoverStarted{}, errors.New("cutover started tuple must be run@RFC3339Z@64hex")
	}
	if !strings.HasSuffix(parts[1], "Z") {
		return CutoverStarted{}, errors.New("cutover started timestamp must use UTC Z")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return CutoverStarted{}, errors.New("cutover started timestamp is invalid")
	}
	return CutoverStarted{RunID: parts[0], StartedAt: startedAt, BaselineDigest: parts[2]}, nil
}
