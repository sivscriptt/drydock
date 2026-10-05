package lint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sivscriptt/drydock/workflow"
)

// build writes a tiny export: start -> n3 lookup -> n4 expression -> end,
// with an optional condition on the lookup -> expression edge.
func build(t *testing.T, gate string, exprs []map[string]string) *workflow.Workflow {
	t.Helper()
	node := func(id int, idx string, ctrl int, params any, typ string) map[string]any {
		return map[string]any{"id": id, "node_index": idx, "workflow_control_id": ctrl, "parameter_map": params,
			"metadata": map[string]any{"type": typ}}
	}
	var gateCond any
	if gate != "" {
		gateCond = gate
	}
	ex := map[string]any{
		"workflow": map[string]any{"name": "t", "version": "1"},
		"nodes": []any{
			node(1, "n0", 1, nil, "start"),
			node(3, "n3", 8, map[string]any{"operation": "select"}, "special"),
			node(4, "n4", 22, map[string]any{"expressions": exprs}, "special"),
			node(9, "n9", 2, nil, "end"),
		},
		"transitions": []any{
			map[string]any{"id": 1, "from_node_id": 1, "to_node_id": 3},
			map[string]any{"id": 2, "from_node_id": 3, "to_node_id": 4, "conditions": gateCond},
			map[string]any{"id": 3, "from_node_id": 3, "to_node_id": 9, "conditions": `$.n3.count == "0"`},
			map[string]any{"id": 4, "from_node_id": 4, "to_node_id": 9},
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

func codes(fs []Finding) map[string]int {
	out := map[string]int{}
	for _, f := range fs {
		out[f.Code]++
	}
	return out
}

// A real outage: a row read guarded only inside ifCondition,
// on a node every application reaches.
var rowRead = []map[string]string{{
	"key":        "reg_ref",
	"expression": `ifCondition( $.{n3.count} > 0, replace('Ref: %', '%', $.{n3.output.0.reference}), '')`,
	"returnType": "string",
}}

func TestUngatedRowReadFails(t *testing.T) {
	fs, st := Workflow(build(t, "", rowRead))
	if codes(fs)["guard-does-not-protect"] != 1 || !fs[0].Fails || st.Expressions != 1 {
		t.Fatalf("findings = %v", fs)
	}
}

func TestGatedRowReadIsSafe(t *testing.T) {
	for _, gate := range []string{`$.n3.count != "0"`, `$.n3.count == "1"`, `$.n3.count > 0`} {
		fs, _ := Workflow(build(t, gate, rowRead))
		if codes(fs)["guard-does-not-protect"] != 0 {
			t.Errorf("gate %s: still flagged: %v", gate, fs)
		}
	}
}

func TestGateThatDoesNotCheckCountDoesNotCount(t *testing.T) {
	fs, _ := Workflow(build(t, `$.n3.state == "success"`, rowRead))
	if codes(fs)["guard-does-not-protect"] != 1 {
		t.Errorf("a non-count gate should not protect: %v", fs)
	}
}

func TestQuotedRowReadCannotCrash(t *testing.T) {
	fs, _ := Workflow(build(t, "", []map[string]string{{
		"key": "flag", "expression": `ifCondition( '$.{n3.output.0.flag}' == '"Yes"', 'a', 'b')`, "returnType": "string",
	}}))
	if len(fs) != 0 {
		t.Errorf("findings = %v", fs)
	}
}

func TestSameNodeReferences(t *testing.T) {
	fs, _ := Workflow(build(t, `$.n3.count != "0"`, []map[string]string{
		{"key": "a", "expression": `1 + 1`, "returnType": "numeric"},
		{"key": "b", "expression": `$.{n4.output.a} * 2`, "returnType": "numeric"}, // earlier sibling
		{"key": "c", "expression": `$.{n4.output.d} * 2`, "returnType": "numeric"}, // later sibling
		{"key": "d", "expression": `3`, "returnType": "numeric"},
	}))
	c := codes(fs)
	if c["same-node-earlier"] != 1 || c["same-node-forward"] != 1 {
		t.Errorf("findings = %v", fs)
	}
	for _, f := range fs {
		if f.Code == "same-node-forward" && !f.Fails || f.Code == "same-node-earlier" && f.Fails {
			t.Errorf("severity wrong: %v", f)
		}
	}
}

func TestSyntaxErrorsInExpressionsAndConditions(t *testing.T) {
	w := build(t, `$.n3.count !=`, []map[string]string{{"key": "x", "expression": `ifCondition(1, 2`, "returnType": "string"}})
	fs, st := Workflow(w)
	if codes(fs)["syntax"] != 2 || st.Conditions != 2 {
		t.Errorf("findings = %v, stats %+v", fs, st)
	}
}

func TestStaleFormSlug(t *testing.T) {
	w := build(t, `$.n3.count != "0"`, []map[string]string{{"key": "a", "expression": `'$.{n7.form-v2.status}' == '"Yes"'`, "returnType": "string"}})
	// Make n3 a task that submits form-v3 only.
	task := w.NodeByIndex("n3")
	task.Control = workflow.CtrlAssignTask
	task.OutputMap = map[string]any{"state": "string", "form-v3": map[string]any{}}
	w.NodeByIndex("n4").Params["expressions"] = []any{map[string]any{"key": "a", "expression": `'$.{n3.form-v2.status}' == '"Yes"'`, "returnType": "string"}}
	fs, _ := Workflow(w)
	if codes(fs)["stale-form-slug"] != 1 {
		t.Errorf("findings = %v", fs)
	}
}
