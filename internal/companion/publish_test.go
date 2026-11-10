package companion

import (
	"encoding/json"
	"os"
	"testing"
)

// taw/core before 1.105.1 wrote a record with no changes (a delete) as
// "changes": []; the plan must still read, those records with no changes.
func TestPublishPlanReadsEmptyChangesArray(t *testing.T) {
	data, err := os.ReadFile("testdata/publish-plan-empty-changes.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan PublishPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatalf("the plan isn't readable: %v", err)
	}
	recs := plan.Plan.Records
	if len(recs) != 5 {
		t.Fatalf("records = %d, want 5", len(recs))
	}
	if c, ok := recs[0].Changes["title"]; !ok || c.Status != "new" || string(c.New) != `"Privacy policy"` {
		t.Errorf("an object reads as before: %+v", recs[0].Changes)
	}
	for _, r := range recs[1:] {
		if r.Op != "would-delete" {
			t.Errorf("%s: op = %q", r.Key, r.Op)
		}
		if r.Changes == nil || len(r.Changes) != 0 {
			t.Errorf("%s: changes = %#v, want an empty map", r.Key, r.Changes)
		}
	}
}

func TestChangesRefusesWhatIsntAnObject(t *testing.T) {
	for _, in := range []string{`[{"status":"new"}]`, `"value"`, `42`, `true`} {
		var c Changes
		if err := json.Unmarshal([]byte(in), &c); err == nil {
			t.Errorf("%s: read as %#v, want an error", in, c)
		}
	}
	var r PlanRecord
	if err := json.Unmarshal([]byte(`{"op":"update","changes":[1]}`), &r); err == nil {
		t.Error("a non-empty array in a record: want an error")
	}
}

func TestChangesWritesAnObject(t *testing.T) {
	out, err := json.Marshal(PlanRecord{Op: "would-delete", Changes: Changes{}})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]json.RawMessage
	_ = json.Unmarshal(out, &back)
	if string(back["changes"]) != "{}" {
		t.Errorf("changes = %s, want {}", back["changes"])
	}
}
