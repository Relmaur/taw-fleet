package actions

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/render"
)

// CreateTask makes a new Local site with a TAW theme. r must be normalized
// (create.Normalize) and checked (create.Check). Local has to be open.
// LocalAPI overrides Local's API (tests).
func (a *Actions) CreateTask(r create.Request) (Task, error) {
	api := a.LocalAPI
	if api == nil {
		g, err := local.NewGraphQL(a.Paths)
		if err != nil {
			return Task{}, err
		}
		api = g
	}
	c := &create.Creator{Paths: a.Paths, Exec: a.Exec, Local: api, Poll: a.CreatePoll, Now: a.Now}
	return Task{Title: "Create site: " + r.Name, Writes: true, Run: func(ctx context.Context, out io.Writer) (Summary, error) {
		res, err := c.Run(ctx, r, out)
		if err != nil {
			return Summary{Report: res}, err
		}
		return summarizeCreate(a, res), nil
	}}, nil
}

func summarizeCreate(a *Actions, res create.Result) Summary {
	lines := []string{
		res.URL + "   " + res.AdminURL,
		"Admin " + res.AdminUser + " · password " + res.AdminPassword + "   (shown once: save it now)",
		fmt.Sprintf("%s in %s", res.Kind.Scaffold(), render.Tilde(a.Paths, res.ThemePath)),
	}
	if res.TawCore != "" {
		lines[2] += " · taw/core " + strings.TrimPrefix(res.TawCore, "v")
	}
	if res.Commit != "" {
		lines[2] += " · first commit " + res.Commit
	}
	for _, w := range res.Warnings {
		lines = append(lines, "! "+w)
	}
	lines = append(lines, "Next: taw-fleet open "+res.Slug+" --editor; add a GitHub remote when you're ready")
	return Summary{Headline: res.Slug + " is ready: " + res.URL, Lines: lines, Report: res, Secret: res.AdminPassword}
}
