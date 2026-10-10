package taw

import (
	"strings"

	"github.com/Relmaur/taw-fleet/internal/composer"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// Step is one step of an update, as the dashboard lists it.
type Step struct {
	Key   string // prepare, branch, core, files, migrations, check:<name>, commit, deliver
	Label string
}

// Step states, as StepEvent.State and the dashboard's checklist use them.
const (
	StepWait = "wait" // not started
	StepRun  = "run"
	StepPass = "pass"
	StepFail = "fail"
	StepSkip = "skip" // not run here (the reason is the detail)
	StepNone = "none" // never reached: the update stopped before it
)

// StepEvent is what one progress line of `bin/taw update` says: a step
// starts (Key set), or the running step ends (Key empty).
type StepEvent struct {
	Key    string
	State  string
	Detail string
}

var checkLabels = map[string]string{
	"lint":    "Lint (PHP syntax)",
	"phpstan": "Static analysis (PHPStan)",
	"test":    "Unit tests",
	"build":   "Front-end build",
	"smoke":   "Smoke test (WordPress)",
}

// NeedsBridge says whether the theme's taw/core predates the one-step
// update, so taw-fleet gets it into vendor/ first (Runner.Update).
func NeedsBridge(t site.Theme) bool {
	installed, _ := composer.InstalledVersion(t.RealPath, composer.CorePackage)
	return installed == "" || composer.Older(installed, MinOneStep)
}

// UpdatePlan lists the steps an update of the theme will go through, as its
// policy says: the dashboard's checklist and progress bar.
func UpdatePlan(t site.Theme, pol Policy, bridge bool) []Step {
	var steps []Step
	if bridge {
		steps = append(steps, Step{"prepare", "Get the one-step update"})
	}
	steps = append(steps, Step{"branch", "New branch"}, Step{"core", "Update taw/core"})
	if t.Kind == site.KindClassic && pol.Scaffold != "off" {
		steps = append(steps, Step{"files", "Framework files"})
	}
	steps = append(steps, Step{"migrations", "Migrations"})
	for _, c := range pol.Checks {
		label := checkLabels[c]
		if label == "" {
			label = "Check: " + c
		}
		steps = append(steps, Step{"check:" + c, label})
	}
	steps = append(steps, Step{"commit", "Commit"})
	switch pol.Deliver {
	case "branch":
	case "pr+merge":
		steps = append(steps, Step{"deliver", "Pull request (merges itself)"})
	default:
		steps = append(steps, Step{"deliver", "Pull request"})
	}
	return steps
}

// ParseProgress reads one progress line of `bin/taw update` (taw/core's
// Updater, whose wording its UpdaterTest pins) or of Runner.Update's own
// first step.
func ParseProgress(line string) (StepEvent, bool) {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.Contains(line, "has no one-step update: getting the newest into vendor/"):
		return StepEvent{Key: "prepare", State: StepRun}, true
	case strings.HasPrefix(line, "Working on a new branch, "):
		detail := strings.TrimSuffix(strings.TrimPrefix(line, "Working on a new branch, "), ")")
		return StepEvent{Key: "branch", State: StepRun, Detail: strings.Replace(detail, " (from ", " from ", 1)}, true
	case strings.HasPrefix(line, "Updating taw/core"):
		return StepEvent{Key: "core", State: StepRun}, true
	case strings.HasPrefix(line, "taw/core stays at "):
		return StepEvent{Key: "core", State: StepSkip, Detail: strings.TrimPrefix(line, "taw/core ")}, true
	case line == "Applying the framework files":
		return StepEvent{Key: "files", State: StepRun}, true
	case line == "Running the migrations":
		return StepEvent{Key: "migrations", State: StepRun}, true
	case strings.HasPrefix(line, "Check: "):
		return StepEvent{Key: "check:" + strings.TrimPrefix(line, "Check: "), State: StepRun}, true
	case strings.HasPrefix(line, "Pushing ") && strings.HasSuffix(line, " and opening a pull request"):
		return StepEvent{Key: "deliver", State: StepRun}, true
	case strings.HasPrefix(trimmed, "✓ "):
		return StepEvent{State: StepPass}, true
	case strings.HasPrefix(trimmed, "✗ "):
		return StepEvent{State: StepFail}, true
	case strings.HasPrefix(trimmed, "– "):
		detail := trimmed
		if i := strings.Index(trimmed, "not run here ("); i >= 0 {
			detail = strings.TrimSuffix(trimmed[i+len("not run here ("):], ")")
			if j := strings.Index(detail, ": "); j > 0 { // the reason, not the how-to after it
				detail = detail[:j]
			}
		}
		return StepEvent{State: StepSkip, Detail: detail}, true
	}
	return StepEvent{}, false
}

// FailedStep maps the report's failure step to the checklist's key.
func FailedStep(step string) string {
	switch step {
	case "composer":
		return "core"
	case "sync":
		return "files"
	case "migration":
		return "migrations"
	case "branch":
		return "branch"
	case "lint", "phpstan", "test", "build", "smoke":
		return "check:" + step
	}
	return step
}
