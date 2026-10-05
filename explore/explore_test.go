package explore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sivscriptt/drydock/sim"
	"github.com/sivscriptt/drydock/workflow"
)

func licence(t *testing.T) (*workflow.Workflow, *sim.Case) {
	t.Helper()
	w, _, err := workflow.Load("../testdata/licence-bundle")
	if err != nil {
		t.Fatal(err)
	}
	c, err := sim.LoadCase("../testdata/cases/submission.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return w, c
}

func TestEveryRouteOfTheLicenceWorkflow(t *testing.T) {
	w, base := licence(t)
	cfg := DefaultConfig()
	cfg.Mode = All
	res := Run(context.Background(), w, base, cfg)

	if len(res.Scenarios) != 4 || res.Truncated != "" {
		t.Fatalf("got %d scenarios (truncated %q)", len(res.Scenarios), res.Truncated)
	}
	want := map[string]string{
		"n2: officer Cancelled":                                                   "Rejected",
		"n2: officer Completed · n3: lookup finds a row":                          "Rejected",
		"n2: officer Completed · n3: lookup finds nothing · n4: officer Approved": "Licence approved",
		"n2: officer Completed · n3: lookup finds nothing · n4: officer Rejected": "Rejected",
	}
	for _, sc := range res.Scenarios {
		key := strings.Join(sc.Decisions, " · ")
		if want[key] != sc.FlowState || sc.Status != sim.Completed {
			t.Errorf("%s: %s %q", key, sc.Status, sc.FlowState)
		}
	}
	c := res.Coverage
	if c.NodesHit != c.Nodes || c.EdgesTaken != c.Edges || len(c.Untaken) != 0 {
		t.Errorf("coverage %+v", c)
	}
}

// The same workflow must give the same report whatever the parallelism.
func TestDeterministicAcrossWorkerCounts(t *testing.T) {
	w, base := licence(t)
	var first string
	for _, workers := range []int{1, 2, 8, 32} {
		for _, mode := range []Mode{All, Each} {
			cfg := DefaultConfig()
			cfg.Workers, cfg.Mode = workers, mode
			res := Run(context.Background(), w, base, cfg)
			b, _ := json.Marshal(struct {
				S []*Scenario
				C Coverage
			}{res.Scenarios, res.Coverage})
			if mode == All {
				if first == "" {
					first = string(b)
				} else if string(b) != first {
					t.Fatalf("workers=%d gave a different result", workers)
				}
			}
		}
	}
}

// Each mode tries every answer at least once with fewer scenarios.
func TestEachModeCoversWithFewerScenarios(t *testing.T) {
	w, base := licence(t)
	cfg := DefaultConfig()
	cfg.Mode = Each
	res := Run(context.Background(), w, base, cfg)
	if res.Coverage.EdgesTaken != res.Coverage.Edges {
		t.Errorf("each mode coverage %+v", res.Coverage)
	}
	if len(res.Scenarios) > 4 {
		t.Errorf("each mode should not need more than all: %d", len(res.Scenarios))
	}
}

// A workflow where an empty lookup fails an expression: the explorer must
// find it without being told, and group it as one finding.
func TestFindsTheFailureOnItsOwn(t *testing.T) {
	ex := map[string]any{
		"workflow":   map[string]any{"name": "t", "version": "1"},
		"variables":  map[string]string{"id": "string"},
		"triggerMap": map[string]any{"id": map[string]string{"value": "$.meta.user_identifier"}},
		"nodes": []any{
			map[string]any{"id": 1, "node_index": "n0", "workflow_control_id": 1, "metadata": map[string]any{"type": "start"}},
			map[string]any{"id": 2, "node_index": "n2", "workflow_control_id": 5, "parameter_map": map[string]any{"title": "Check"}},
			map[string]any{"id": 3, "node_index": "n3", "workflow_control_id": 8, "parameter_map": map[string]any{"operation": "select", "table_name": "registry"}},
			map[string]any{"id": 4, "node_index": "n4", "workflow_control_id": 22, "parameter_map": map[string]any{"expressions": []any{
				map[string]any{"key": "ref", "expression": `ifCondition( $.{n3.count} > 0, replace('Ref: %', '%', $.{n3.output.0.reference}), '')`, "returnType": "string"},
			}}},
			map[string]any{"id": 9, "node_index": "n9", "workflow_control_id": 2},
			map[string]any{"id": 8, "node_index": "n8", "workflow_control_id": 2},
		},
		"transitions": []any{
			map[string]any{"id": 1, "from_node_id": 1, "to_node_id": 2},
			map[string]any{"id": 2, "from_node_id": 2, "to_node_id": 3, "conditions": `$.n2.state == "Completed"`},
			map[string]any{"id": 5, "from_node_id": 2, "to_node_id": 8, "conditions": `$.n2.state == "Cancelled"`},
			map[string]any{"id": 3, "from_node_id": 3, "to_node_id": 4},
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
	res := Run(context.Background(), w, &sim.Case{}, DefaultConfig())
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	f := res.Findings[0]
	if f.Status != sim.Failed || f.At != "n4" || !strings.Contains(strings.Join(f.Example.Decisions, " "), "n3: lookup finds nothing") {
		t.Errorf("finding = %+v, example %v", f, f.Example.Decisions)
	}
}

func TestTimeoutStopsAndSaysSo(t *testing.T) {
	w, base := licence(t)
	cfg := DefaultConfig()
	cfg.Mode = All
	cfg.Timeout = time.Nanosecond
	res := Run(context.Background(), w, base, cfg)
	if !strings.Contains(res.Truncated, "time limit") {
		t.Errorf("truncated = %q", res.Truncated)
	}
}

func TestCancelledContext(t *testing.T) {
	w, base := licence(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := DefaultConfig()
	cfg.Mode, cfg.Timeout = All, 0
	if res := Run(ctx, w, base, cfg); res.Truncated == "" {
		t.Error("a cancelled run should report it did not finish")
	}
}

func TestAutoFallsBackToEach(t *testing.T) {
	w, base := licence(t)
	cfg := DefaultConfig()
	cfg.Mode, cfg.MaxRuns = Auto, 2
	res := Run(context.Background(), w, base, cfg)
	if res.Mode != Each || res.Fallback == "" {
		t.Errorf("mode %s, fallback %q", res.Mode, res.Fallback)
	}
}
