package sim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sivscriptt/drydock/workflow"
)

func licence(t *testing.T) *workflow.Workflow {
	t.Helper()
	w, _, err := workflow.Load("../testdata/licence-bundle")
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func caseFile(t *testing.T, name string) *Case {
	t.Helper()
	c, err := LoadCase("../testdata/cases/" + name + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func inlineCase(t *testing.T, yaml string) *Case {
	t.Helper()
	p := filepath.Join(t.TempDir(), "case.yaml")
	os.WriteFile(p, []byte(yaml), 0o644)
	c, err := LoadCase(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func path(run *Run) string {
	var p []string
	for _, s := range run.Steps {
		p = append(p, s.Node)
	}
	return strings.Join(p, " ")
}

func TestApproved(t *testing.T) {
	run := Simulate(licence(t), caseFile(t, "approved"), DefaultOptions())
	if run.Status != Completed {
		t.Fatalf("%s: %s", run.Status, run.Reason)
	}
	if got := path(run); got != "n0 n1 n2 n3 n4 n5 n6 n7" {
		t.Errorf("path = %s", got)
	}
	if run.FlowState != "Licence approved" {
		t.Errorf("flow state = %q", run.FlowState)
	}

	// Two boats, two children each; the per-iteration lookup gives 0 then 1 rows.
	var loop Step
	for _, s := range run.Steps {
		if s.Node == "n5" {
			loop = s
		}
	}
	if len(loop.Children) != 4 {
		t.Fatalf("loop children = %d", len(loop.Children))
	}
	if !strings.Contains(loop.Children[0].Detail, "no rows") || !strings.Contains(loop.Children[2].Detail, "1 row") {
		t.Errorf("per-iteration lookups: %q / %q", loop.Children[0].Detail, loop.Children[2].Detail)
	}

	// The task title is filled in from the trigger variables.
	if !strings.Contains(run.Steps[2].Detail, "A123456") {
		t.Errorf("task detail = %q", run.Steps[2].Detail)
	}
}

func TestRejectedByLookup(t *testing.T) {
	run := Simulate(licence(t), caseFile(t, "already-licensed"), DefaultOptions())
	if run.Status != Completed || run.FlowState != "Rejected" || !strings.HasSuffix(path(run), "n3 n8 n9") {
		t.Errorf("%s %q %s", run.Status, run.FlowState, path(run))
	}
}

func TestWaitingSaysWhatIsNeeded(t *testing.T) {
	run := Simulate(licence(t), caseFile(t, "undecided"), DefaultOptions())
	if run.Status != Waiting || run.At != "n4" || !strings.Contains(run.Reason, "Approved, Rejected") {
		t.Errorf("%s at %s: %s", run.Status, run.At, run.Reason)
	}
}

func TestHandshakeFailure(t *testing.T) {
	// island is a string variable; a number in the submission never makes an instance.
	c := inlineCase(t, "submission:\n  attributes: {island: 5}\n  meta: {user_identifier: A1}\n")
	run := Simulate(licence(t), c, DefaultOptions())
	if run.Status != Rejected || !strings.Contains(run.Reason, "handshake") || len(run.Steps) != 0 {
		t.Errorf("%s: %s", run.Status, run.Reason)
	}
}

func TestStuckExplainsEveryCondition(t *testing.T) {
	c := inlineCase(t, "submission: {attributes: {island: Male}, meta: {user_identifier: A1}}\ntasks: {n2: {state: On Hold}}\n")
	run := Simulate(licence(t), c, DefaultOptions())
	if run.Status != Stuck || run.At != "n2" {
		t.Fatalf("%s at %s", run.Status, run.At)
	}
	for _, want := range []string{"none of its 2 transitions", `n2.state is "On Hold"`, "to n8", "to n3"} {
		if !strings.Contains(run.Reason, want) {
			t.Errorf("reason missing %q:\n%s", want, run.Reason)
		}
	}
}

// A small workflow built in the test: start -> lookup n3 -> expression n4 -> end.
func lookupThenExpression(t *testing.T, expression string) *workflow.Workflow {
	t.Helper()
	ex := map[string]any{
		"workflow":   map[string]any{"name": "t", "version": "1"},
		"variables":  map[string]string{"id": "string"},
		"triggerMap": map[string]any{"id": map[string]string{"value": "$.meta.user_identifier"}},
		"nodes": []any{
			map[string]any{"id": 1, "node_index": "n0", "workflow_control_id": 1, "metadata": map[string]any{"type": "start"}},
			map[string]any{"id": 3, "node_index": "n3", "workflow_control_id": 8, "parameter_map": map[string]any{"operation": "select", "table_name": "registry"}},
			map[string]any{"id": 4, "node_index": "n4", "workflow_control_id": 22, "parameter_map": map[string]any{"expressions": []any{
				map[string]any{"key": "ref", "expression": expression, "returnType": "string"},
			}}},
			map[string]any{"id": 9, "node_index": "n9", "workflow_control_id": 2},
		},
		"transitions": []any{
			map[string]any{"id": 1, "from_node_id": 1, "to_node_id": 3},
			map[string]any{"id": 2, "from_node_id": 3, "to_node_id": 4},
			map[string]any{"id": 3, "from_node_id": 4, "to_node_id": 9},
		},
	}
	b, _ := json.Marshal(ex)
	p := filepath.Join(t.TempDir(), "w.json")
	os.WriteFile(p, b, 0o644)
	w, _, err := workflow.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// A real outage, in miniature: the guarded row read passes when the
// applicant is registered and fails the instance when they are not.
func TestGuardedRowReadFailsOnlyWhenLookupIsEmpty(t *testing.T) {
	w := lookupThenExpression(t, `ifCondition( $.{n3.count} > 0, replace('Ref: %', '%', $.{n3.output.0.reference}), '')`)

	registered := inlineCase(t, "submission: {meta: {user_identifier: A1}}\nlookups: {n3: [{reference: R-1}]}\n")
	if run := Simulate(w, registered, DefaultOptions()); run.Status != Completed {
		t.Errorf("registered: %s %s", run.Status, run.Reason)
	}

	unregistered := inlineCase(t, "submission: {meta: {user_identifier: A2}}\nlookups: {n3: []}\n")
	run := Simulate(w, unregistered, DefaultOptions())
	if run.Status != Failed || run.At != "n4" || !strings.Contains(run.Reason, "unresolved reference $.{n3.output.0.reference}") {
		t.Errorf("unregistered: %s at %s: %s", run.Status, run.At, run.Reason)
	}
}

func TestUnlistedColumnsAreNull(t *testing.T) {
	w := lookupThenExpression(t, `ifCondition( $.{n3.count} > 0, '$.{n3.output.0.other}', '')`)
	c := inlineCase(t, "submission: {meta: {user_identifier: A1}}\nlookups: {n3: [{reference: R-1}]}\n")
	run := Simulate(w, c, DefaultOptions())
	row := run.Context["n3"].(map[string]any)["output"].([]any)[0].(map[string]any)
	if _, ok := row["other"]; !ok || run.Status != Completed {
		t.Errorf("row = %v, status %s %s", row, run.Status, run.Reason)
	}
}

func TestForkAndRunaway(t *testing.T) {
	w := licence(t)
	n1, n3 := w.NodeByIndex("n1"), w.NodeByIndex("n3")
	c := caseFile(t, "approved")

	// Give n1 a second unconditional way out.
	n1.Out = append(n1.Out, &workflow.Transition{From: n1, To: n3})
	first := Simulate(w, c, DefaultOptions())
	if first.Status != Completed || len(first.Notes) == 0 || !strings.Contains(strings.Join(first.Notes, " "), "took the first") {
		t.Errorf("first match: %s, notes %v", first.Status, first.Notes)
	}
	opt := DefaultOptions()
	opt.Fork = AllMatches
	both := Simulate(w, c, opt)
	legs := map[int]bool{}
	for _, s := range both.Steps {
		legs[s.Leg] = true
	}
	if len(legs) != 2 {
		t.Errorf("fork all: legs %v", legs)
	}

	// A cycle with no exit.
	n6 := w.NodeByIndex("n6")
	n6.Out = []*workflow.Transition{{From: n6, To: w.NodeByIndex("n0")}}
	opt = DefaultOptions()
	opt.MaxSteps = 50
	if run := Simulate(w, c, opt); run.Status != Runaway {
		t.Errorf("cycle: %s", run.Status)
	}
}

func TestCaseFileTyposAreRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(p, []byte("submision: {}\n"), 0o644)
	if _, err := LoadCase(p); err == nil {
		t.Error("misspelt key should be an error")
	}
}

func TestMessagesMarkBlanks(t *testing.T) {
	ctx := &scope{data: map[string]any{"name": "Ali", "ref": "", "island": nil}}
	got := markBlanks("Dear $.{name}, ref $.{ref} from $.{island} ($.{nobody}).", ctx)
	if got != "Dear Ali, ref [blank] from [blank] ([blank])." {
		t.Errorf("got %q", got)
	}
}
