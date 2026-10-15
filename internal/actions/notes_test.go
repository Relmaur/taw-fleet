package actions

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/notes"
)

func TestNotedWritesWhatATaskDid(t *testing.T) {
	a, _ := setup(t, config.Config{})
	run := func(sum Summary, err error, ctx context.Context) {
		t.Helper()
		task := a.noted("acme", Task{Title: "Update acme", Run: func(context.Context, io.Writer) (Summary, error) { return sum, err }})
		got, gotErr := task.Run(ctx, io.Discard)
		if !errors.Is(gotErr, err) || got.Headline != sum.Headline {
			t.Errorf("the task's own result must come back: %+v %v", got, gotErr)
		}
	}
	ctx := context.Background()
	run(Summary{Headline: "acme: updated to taw/core 1.99.1"}, nil, ctx)
	run(Summary{Headline: "acme: nothing to update", Unnoted: true}, nil, ctx)
	run(Summary{Headline: "Merged #3 into main", Note: "Merged #3 (Fix the hero) into main"}, nil, ctx)
	run(Summary{}, errors.New("composer update failed\nlots of output"), ctx)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	run(Summary{}, context.Canceled, cancelled)

	es, err := notes.Read(a.NotesFile("acme"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		if e.By != notes.TawFleet {
			t.Errorf("by %q", e.By)
		}
		got = append(got, e.Text)
	}
	want := "Update acme stopped: composer update failed | Merged #3 (Fix the hero) into main | acme: updated to taw/core 1.99.1"
	if strings.Join(got, " | ") != want {
		t.Errorf("notes:\n%s\nwant\n%s", strings.Join(got, " | "), want)
	}
}

func TestSkillPromptCarriesTheNotes(t *testing.T) {
	a, _ := setup(t, config.Config{})
	s, th := fixture()
	if err := a.AddNote(s.Slug, notes.You, "Client asked to hold the redesign until November."); err != nil {
		t.Fatal(err)
	}
	p := a.SkillPrompt(s, th, Skill{Name: "resolve-comments"}, nil)
	if !strings.Contains(p.Text, "you: Client asked to hold the redesign until November.") || !strings.Contains(p.Text, "taw-fleet notes ls-mxico --by claude") {
		t.Errorf("prompt:\n%s", p.Text)
	}
}
