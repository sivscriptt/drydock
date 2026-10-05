package viewer

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/sivscriptt/drydock/explore"
	"github.com/sivscriptt/drydock/sim"
	"github.com/sivscriptt/drydock/workflow"
)

func licence(t *testing.T) *Model {
	t.Helper()
	w, _, err := workflow.Load("../testdata/licence-bundle")
	if err != nil {
		t.Fatal(err)
	}
	base, err := sim.LoadCase("../testdata/cases/submission.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := explore.DefaultConfig()
	cfg.Mode = explore.All
	return Build(w, explore.Run(context.Background(), w, base, cfg))
}

func TestBuild(t *testing.T) {
	m := licence(t)
	// 13 nodes less the two loop children (drawn inside their loop card)
	// and the unreachable note n10.
	if len(m.Nodes) != 10 || len(m.Scenarios) != 4 {
		t.Fatalf("%d nodes, %d scenarios", len(m.Nodes), len(m.Scenarios))
	}
	kinds := map[string]string{}
	for _, n := range m.Nodes {
		kinds[n.ID] = n.Kind
	}
	for id, want := range map[string]string{"n0": "start", "n2": "task", "n3": "lookup", "n1": "expression", "n5": "loop", "n6": "message", "n7": "end"} {
		if kinds[id] != want {
			t.Errorf("%s kind = %q, want %q", id, kinds[id], want)
		}
	}
	var approved *Scenario
	for i := range m.Scenarios {
		if m.Scenarios[i].Title == "Licence approved" {
			approved = &m.Scenarios[i]
		}
	}
	if approved == nil {
		t.Fatal("no approved scenario")
	}
	// Each step names the transition it took, and the public status it set.
	var sawStatus bool
	for _, s := range approved.Steps {
		if s.Node == "n4" && (s.Next != "n5" || s.Status != "Licence approved") {
			t.Errorf("n4 step = %+v", s)
		}
		if s.Status != "" {
			sawStatus = true
		}
		if s.Node == "n5" && s.Loop != 2 {
			t.Errorf("loop iterations = %d", s.Loop)
		}
	}
	if !sawStatus || m.Coverage.Visits["n0"] != 4 {
		t.Errorf("status %v, visits %v", sawStatus, m.Coverage.Visits)
	}
}

func TestShortCondition(t *testing.T) {
	for in, want := range map[string]string{
		`$.n4.state == "Approved"`: "state = Approved",
		// Long labels are clipped; the full condition shows on hover.
		`$.n19.state == "Completed" AND $.n19.approval-v2.approvalStatus != "Not Approved"`: "state = Completed and approvalStatus != Not App…",
		``: ``,
	} {
		if got := shortCondition(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestRenderIsOneSelfContainedPage(t *testing.T) {
	m := licence(t)
	m.Workflow = `Licence </script><script>alert(1)</script>`
	var buf bytes.Buffer
	if err := Render(&buf, m); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	for _, want := range []string{"<!doctype html>", "dagre", `id="data"`, "Drydock"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// No external fetches: everything is inline.
	if regexp.MustCompile(`(?i)<script[^>]+src=|<link[^>]+href=`).MatchString(page) {
		t.Error("page loads something external")
	}
	// A workflow name cannot close the data script.
	data := page[strings.Index(page, `id="data"`):]
	data = data[strings.Index(data, ">")+1 : strings.Index(data, "</script>")]
	var back Model
	if err := json.Unmarshal([]byte(strings.ReplaceAll(data, `<\/`, `</`)), &back); err != nil {
		t.Fatalf("embedded data does not parse: %v", err)
	}
	if back.Workflow != m.Workflow {
		t.Errorf("workflow name = %q", back.Workflow)
	}
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{
		"Verify application ( $.{applicantID} )":                                   "Verify application",
		"Verification - Fishing Licence ( $.{applicantID} ) - ( $.{otherID} )": "Verification - Fishing Licence",
		"Approve licence": "Approve licence",
		"$.{x}":           "$.{x}",
	} {
		if got := displayName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
