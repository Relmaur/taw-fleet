package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/site"
)

// inspectReport is the part of `bin/taw inspect --json` shown as a summary.
type inspectReport struct {
	TawCoreVersion string `json:"taw_core_version"`
	Blocks         []struct {
		ID     string            `json:"id"`
		Fields []json.RawMessage `json:"fields"`
	} `json:"blocks"`
	Forms []struct {
		ID         string `json:"id"`
		FieldCount int    `json:"field_count"`
		MultiStep  bool   `json:"multi_step"`
	} `json:"forms"`
}

func renderInspect(cmd *cobra.Command, d Deps, t site.Theme, raw json.RawMessage) error {
	var r inspectReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("bin/taw inspect: %w", err)
	}
	p := d.palette()
	muted := p.Fg(p.Muted)
	var b strings.Builder
	b.WriteString("\n " + p.Title(t.Dir) + muted.Render("  ·  taw/core "+strings.TrimPrefix(r.TawCoreVersion, "v")) + "\n\n")
	fmt.Fprintf(&b, " %s %d\n", muted.Width(8).Render("blocks"), len(r.Blocks))
	for _, bl := range r.Blocks {
		fmt.Fprintf(&b, "   %s %s\n", bl.ID, muted.Render(fmt.Sprintf("%d %s", len(bl.Fields), plural(len(bl.Fields), "field", "fields"))))
	}
	fmt.Fprintf(&b, " %s %d\n", muted.Width(8).Render("forms"), len(r.Forms))
	for _, f := range r.Forms {
		extra := ""
		if f.MultiStep {
			extra = ", multi-step"
		}
		fmt.Fprintf(&b, "   %s %s\n", f.ID, muted.Render(fmt.Sprintf("%d %s%s", f.FieldCount, plural(f.FieldCount, "field", "fields"), extra)))
	}
	b.WriteString("\n " + muted.Render("--json for everything bin/taw inspect reports") + "\n")
	_, err := lipgloss.Fprint(cmd.OutOrStdout(), b.String())
	return err
}
