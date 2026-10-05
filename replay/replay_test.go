package replay

import (
	"strings"
	"testing"

	"github.com/sivscriptt/drydock/sim"
	"github.com/sivscriptt/drydock/workflow"
)

func TestReplayMatchesAndCorrectsStaleSnapshot(t *testing.T) {
	w, _, err := workflow.Load("../testdata/licence-bundle")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := Load("../testdata/recordings/approved.context.json", "../testdata/recordings/approved.legs.json")
	if err != nil {
		t.Fatal(err)
	}
	rep := Replay(w, rec, sim.DefaultOptions())

	if !rep.RouteMatches {
		t.Fatalf("route differs at %d\n engine:  %v\n drydock: %v\n%s", rep.Diverge, rep.Recorded, rep.Simulated, rep.Reason)
	}
	if rep.Substitution.Checked != 1 || rep.Substitution.Matched != 1 {
		t.Errorf("substitution = %+v", rep.Substitution)
	}
	// The snapshot said n4 was Rejected; the legs went on to the loop.
	found := false
	for _, s := range rep.Inferred {
		if strings.Contains(s, "n4") {
			found = true
		}
	}
	if !found {
		t.Errorf("stale n4 answer was not corrected from the route: %v", rep.Inferred)
	}
	// Loop children are not part of the top-level route.
	if strings.Contains(strings.Join(rep.Recorded, " "), "lm1") {
		t.Errorf("loop children in route: %v", rep.Recorded)
	}
}

func TestAlign(t *testing.T) {
	got := align(`ifCondition( $.{a} == 'x', $.{b}, 0 )`, `ifCondition( "hi" == 'x', 42, 0 )`)
	if got["a"] != `"hi"` || got["b"] != "42" {
		t.Errorf("align = %v", got)
	}
	if align(`$.{a} + 1`, `totally different`) != nil {
		t.Error("misaligned text should give nothing")
	}
	if align(`no refs`, `no refs`) != nil {
		t.Error("no refs should give nothing")
	}
}

func TestDecodeRendered(t *testing.T) {
	for in, want := range map[string]any{`"No"`: "No", `""`: "", "42": 42.0, "-1.5": -1.5, "true": true} {
		got, ok := decodeRendered(in)
		if !ok || got != want {
			t.Errorf("%s -> %v %v", in, got, ok)
		}
	}
	if _, ok := decodeRendered(`"a" + "b"`); ok {
		t.Error("an expression fragment is not a value")
	}
}

func TestRouteInferenceHelpers(t *testing.T) {
	a := answerFromCondition("n19", `$.n19.state == "Completed" AND $.n19.approval-v2.approvalStatus == "Approved"`)
	if a.State != "Completed" || a.Form["approval-v2"]["approvalStatus"] != "Approved" {
		t.Errorf("answer = %+v", a)
	}
	for cond, want := range map[string]int{`$.n3.count == "0"`: 0, `$.n3.count != "0"`: 1, `$.n3.count == 1`: 1, `$.n3.count > 0`: 1} {
		if got := len(rowsFromCondition("n3", cond)); got != want {
			t.Errorf("%s -> %d rows", cond, got)
		}
	}
}
