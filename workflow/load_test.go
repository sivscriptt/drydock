package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, path string) (*Workflow, []Problem) {
	t.Helper()
	w, problems, err := Load(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return w, problems
}

func codes(problems []Problem) map[string][]string {
	out := map[string][]string{}
	for _, p := range problems {
		out[p.Code] = append(out[p.Code], p.Node)
	}
	return out
}

func TestBundle(t *testing.T) {
	w, problems := load(t, "../testdata/licence-bundle")

	if w.Name != "Fishing Licence - Apply" || w.Version != "1.0.0" {
		t.Errorf("header: %q %q", w.Name, w.Version)
	}
	if w.Start == nil || w.Start.Index != "n0" {
		t.Fatalf("start = %v", w.Start)
	}
	if len(w.Nodes) != 13 || len(w.Transitions) != 11 {
		t.Errorf("got %d nodes, %d transitions", len(w.Nodes), len(w.Transitions))
	}

	// Loop children follow the loop's actions order, not their ids or indexes.
	loop := w.NodeByIndex("n5")
	if loop == nil || len(loop.Children) != 2 || loop.Children[0].Index != "lm1" || loop.Children[1].Index != "lm2" {
		t.Fatalf("loop children = %v", loop.Children)
	}
	if loop.Children[0].Loop != loop || w.NodeByIndex("n5/lm2") != loop.Children[1] {
		t.Error("loop child links")
	}

	// Names fall back from label to task title to the control name.
	if got := w.NodeByIndex("n2").Name; !strings.HasPrefix(got, "Verify application") {
		t.Errorf("task name = %q", got)
	}
	if got := w.NodeByIndex("n3").Name; got != "DataHub" {
		t.Errorf("fallback name = %q", got)
	}

	// PHP's [] and null for empty maps load as empty maps.
	if w.NodeByIndex("n0").Params == nil || w.NodeByIndex("n9").Params == nil {
		t.Error("empty parameter_map should be an empty map")
	}

	if v := w.Variables["island"]; v.Base != "string" || !v.Nullable {
		t.Errorf("variable type = %+v", v)
	}
	if w.Trigger["applicantID"] != "$.meta.user_identifier" {
		t.Errorf("trigger = %v", w.Trigger)
	}
	if len(w.Milestones) != 2 || w.Milestones[0].Name != "Verification" {
		t.Errorf("milestones not in order: %+v", w.Milestones)
	}
	if w.FlowStates[9001].PublicLabel != "Licence approved" {
		t.Errorf("flow states = %+v", w.FlowStates)
	}

	// Conditions and state changes on transitions.
	var approve *Transition
	for _, tr := range w.NodeByIndex("n4").Out {
		if tr.To.Index == "n5" {
			approve = tr
		}
	}
	if approve == nil || approve.Condition != `$.n4.state == "Approved"` || approve.StateChange == nil || *approve.StateChange != 9001 {
		t.Errorf("approve transition = %+v", approve)
	}

	c := codes(problems)
	if got := c["unreachable"]; len(got) != 1 || got[0] != "n10" {
		t.Errorf("unreachable = %v", got)
	}
	if len(c["inactive-transitions"]) != 1 {
		t.Errorf("inactive transitions not reported: %v", problems)
	}
	if len(c["missing-loop-child"]) != 0 || len(c["dead-end"]) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
}

func TestPlainExportMissesLoopChildren(t *testing.T) {
	w, problems := load(t, "../testdata/licence.export.json")
	if len(w.NodeByIndex("n5").Children) != 0 {
		t.Error("plain export should have no loop children")
	}
	got := codes(problems)["missing-loop-child"]
	if len(got) != 2 || got[0] != "n5" {
		t.Errorf("missing-loop-child = %v", got)
	}
	// The loader still says how to get them.
	for _, p := range problems {
		if p.Code == "missing-loop-child" && !strings.Contains(p.Message, "get_workflow_nodes(parent_id=105)") {
			t.Errorf("message does not say how to fix: %s", p.Message)
		}
	}
}

func TestBundleAcceptsAnyLevel(t *testing.T) {
	for _, dir := range []string{"../testdata/licence-bundle", "../testdata/licence-bundle/raw", "../testdata/licence-bundle/raw/workflows"} {
		if _, _, err := Load(dir); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
}

func TestCheckFindsDeadEndsAndAmbiguousForks(t *testing.T) {
	w, _ := load(t, "../testdata/licence-bundle")

	// Cut the way out of the approved SMS, and give n1 a second
	// unconditional transition.
	sms := w.NodeByIndex("n6")
	sms.Out = nil
	n1 := w.NodeByIndex("n1")
	n1.Out = append(n1.Out, &Transition{From: n1, To: w.NodeByIndex("n3")})

	c := codes(Check(w))
	if got := c["dead-end"]; len(got) != 1 || got[0] != "n6" {
		t.Errorf("dead-end = %v", got)
	}
	if got := c["multiple-unconditional"]; len(got) != 1 || got[0] != "n1" {
		t.Errorf("multiple-unconditional = %v", got)
	}
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	if _, _, err := Load(write("empty.json", `{"workflow":{},"nodes":[]}`)); err == nil {
		t.Error("export with no nodes should fail")
	}
	if _, _, err := Load(write("bad.json", `{`)); err == nil {
		t.Error("bad JSON should fail")
	}
	// No start node: a single task with an incoming edge.
	nostart := `{"workflow":{"name":"x"},"nodes":[{"id":1,"node_index":"n1","workflow_control_id":5},{"id":2,"node_index":"n2","workflow_control_id":2}],
		"transitions":[{"id":1,"from_node_id":2,"to_node_id":1},{"id":2,"from_node_id":1,"to_node_id":2}]}`
	if _, problems, err := Load(write("nostart.json", nostart)); err == nil || codes(problems)["no-start"] == nil {
		t.Errorf("no start: %v %v", err, problems)
	}
	if _, _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("missing file should fail")
	}
}

// Real exports are not part of the repo. Point DRYDOCK_REAL at a list of
// export files or bundle folders (separated by the OS path list separator)
// to check them too.
func TestRealExports(t *testing.T) {
	paths := filepath.SplitList(os.Getenv("DRYDOCK_REAL"))
	if len(paths) == 0 {
		t.Skip("set DRYDOCK_REAL to run against real exports")
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			w, problems := load(t, p)
			reach := w.Reachable()
			for _, pr := range problems {
				t.Log(pr)
			}
			t.Logf("%s v%s: %d nodes, %d reachable, %d transitions", w.Name, w.Version, len(w.Nodes), len(reach), len(w.Transitions))
			for _, tr := range w.Transitions {
				if tr.From == nil || tr.To == nil {
					t.Fatalf("transition %d not linked", tr.ID)
				}
			}
		})
	}
}

func TestCompactFormatIncludesLoopChildren(t *testing.T) {
	w, problems := load(t, "../testdata/licence.compact.json")
	loop := w.NodeByIndex("n5")
	if len(loop.Children) != 2 || loop.Children[0].Index != "lm1" {
		t.Fatalf("children = %v", loop.Children)
	}
	if len(w.Transitions) != 11 || w.Trigger["island"] != "$.attributes.island" || len(w.Milestones) != 2 {
		t.Errorf("compact load: %d transitions, trigger %v", len(w.Transitions), w.Trigger)
	}
	if codes(problems)["missing-loop-child"] != nil {
		t.Errorf("problems: %v", problems)
	}
}

func TestUnknownFormatIsRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "odd.json")
	os.WriteFile(p, []byte(`{"workflow":{"name":"x"},"nodes":[{"id":1,"node_index":"n0","workflow_control_id":1}]}`), 0o644)
	if _, _, err := Load(p); err == nil || !strings.Contains(err.Error(), "not a workflow export") {
		t.Errorf("err = %v", err)
	}
}
