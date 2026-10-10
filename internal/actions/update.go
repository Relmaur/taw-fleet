package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Relmaur/taw-fleet/internal/handoff"
	"github.com/Relmaur/taw-fleet/internal/taw"
	"github.com/Relmaur/taw-fleet/internal/tools"
)

// FixPrompt is "Fix with Claude": the update's report, which is also the
// guide a person follows (umbrella ADR-0004), with what Claude may do.
func FixPrompt(o UpdateOutcome) (handoff.Prompt, error) {
	if o.Report.ReportPath == "" {
		return handoff.Prompt{}, errors.New("the update wrote no report to work from")
	}
	md, err := os.ReadFile(o.Report.ReportPath)
	if err != nil {
		return handoff.Prompt{}, err
	}
	text := fmt.Sprintf(`# Finish the TAW update of %[1]s

The update stopped, and nothing was pushed. Its report is below (also in %[2]s): what failed, why that usually happens, and the steps to fix, verify, finish and undo it. A person would follow the same steps.

- Work on the branch %[3]s; it's checked out. Change only what the steps need.
- Run the failed check again until it passes, then commit on the branch.
- Before pushing or opening the pull request (the report's "Finish"), say what you changed and ask.
- Never merge: merging deploys production.
- If a fix needs a decision only the site's owner can make, stop and say so.

---

%[4]s`, o.Theme.Dir, taw.ReportFile, o.Report.Branch, md)
	return handoff.Prompt{Title: "Finish the update of " + o.Theme.Dir, Branch: o.Report.Branch, Text: text}, nil
}

// FixUpdate opens Claude Code in the theme folder on the update's report.
func (a *Actions) FixUpdate(ctx context.Context, o UpdateOutcome, beside string) (Launched, error) {
	p, err := FixPrompt(o)
	if err != nil {
		return Launched{}, err
	}
	return a.LaunchSkill(ctx, o.Site, o.Theme, "update-fix", p, beside)
}

// OpenURL opens a link (an update's pull request) in the browser.
func (a *Actions) OpenURL(ctx context.Context, url string) (string, error) {
	return "Opened " + url, a.open.URL(ctx, url)
}

// OpenGuide is "Do it myself": the update's report in the editor.
func (a *Actions) OpenGuide(ctx context.Context, o UpdateOutcome) (string, error) {
	path := o.Report.ReportPath
	if path == "" {
		path = filepath.Join(o.Theme.RealPath, taw.ReportFile)
	}
	if !fileExists(path) {
		return "", errors.New("the update wrote no report")
	}
	app, err := tools.Pick(a.Tools.Editors, a.Config.Editor)
	if err != nil {
		return "", fmt.Errorf("editor: %w", err)
	}
	return fmt.Sprintf("Opened the guide (%s) in %s", taw.ReportFile, app.Name), a.open.With(ctx, app, path)
}
