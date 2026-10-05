// Package workflow loads OneGov workflow exports into a graph that the
// simulator can walk.
package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Control is a node's workflow_control_id.
type Control int

const (
	CtrlNoAction      Control = 1 // start, and the router/merge "no action" node
	CtrlEnd           Control = 2
	CtrlInfo          Control = 3
	CtrlSMS           Control = 4
	CtrlAssignTask    Control = 5
	CtrlEmail         Control = 6
	CtrlExternalState Control = 7
	CtrlDataHub       Control = 8
	CtrlG2G           Control = 9
	CtrlGenerateFile  Control = 10
	CtrlEDMS          Control = 11
	CtrlExpressionV1  Control = 12
	CtrlInvoice       Control = 13
	CtrlLocker        Control = 14
	CtrlDCN           Control = 15
	CtrlRunQuery      Control = 16
	CtrlPaymentStatus Control = 17
	CtrlNotes         Control = 18
	CtrlFormat        Control = 19
	CtrlGenerateFile2 Control = 20
	CtrlLoop          Control = 21
	CtrlExpressionV2  Control = 22
)

var controlNames = map[Control]string{
	CtrlNoAction: "No Action", CtrlEnd: "End", CtrlInfo: "Info", CtrlSMS: "SMS",
	CtrlAssignTask: "Assign Task", CtrlEmail: "Email", CtrlExternalState: "External State",
	CtrlDataHub: "DataHub", CtrlG2G: "G2G", CtrlGenerateFile: "Generate File", CtrlEDMS: "EDMS",
	CtrlExpressionV1: "Expression v1", CtrlInvoice: "Generate Invoice", CtrlLocker: "Locker",
	CtrlDCN: "Generate DCN", CtrlRunQuery: "Run Query", CtrlPaymentStatus: "Payment Status",
	CtrlNotes: "Notes", CtrlFormat: "Format", CtrlGenerateFile2: "Generate File v2",
	CtrlLoop: "Loop", CtrlExpressionV2: "Expression v2",
}

func (c Control) String() string {
	if n, ok := controlNames[c]; ok {
		return n
	}
	return fmt.Sprintf("Control %d", int(c))
}

// Terminal reports whether a node of this control ends a path.
func (c Control) Terminal() bool { return c == CtrlEnd || c == CtrlFormat }

// Workflow is a loaded workflow graph.
type Workflow struct {
	ID      int64
	UUID    string
	Name    string
	Version string

	// Trigger maps a workflow variable to where it comes from when the
	// workflow starts, e.g. "bank" -> "$.attributes.bank".
	Trigger   map[string]string
	Variables map[string]VarType

	Nodes       []*Node // sorted by node index number (creation order, not run order)
	Transitions []*Transition
	Start       *Node

	Milestones []Milestone
	FlowStates map[int64]FlowState

	byID    map[int64]*Node
	byIndex map[string]*Node
}

// Node is one step in the workflow.
type Node struct {
	ID        int64
	UUID      string
	Index     string // "n12", or "l1"/"lm1" for loop children
	Control   Control
	Name      string
	Milestone *int64
	Params    map[string]any
	OutputMap any

	// Loop is set on a loop's child nodes; Children (in run order) on the loop.
	Loop     *Node
	Children []*Node

	Out []*Transition
	In  []*Transition
}

func (n *Node) String() string { return fmt.Sprintf("%s %s", n.Index, n.Name) }

// Transition is a directed edge. An empty Condition means always.
type Transition struct {
	ID          int64
	From, To    *Node
	Condition   string
	StateChange *int64 // flow state the instance moves to when taken
}

type Milestone struct {
	ID    int64
	Name  string
	Order int
}

type FlowState struct {
	ID          int64
	Label       string
	PublicLabel string
}

// VarType is a declared variable type such as "numeric|nullable".
type VarType struct {
	Raw      string
	Base     string // string, numeric, file, array, ...
	Nullable bool
}

func parseVarType(raw string) VarType {
	v := VarType{Raw: raw}
	for _, part := range strings.Split(raw, "|") {
		switch part = strings.TrimSpace(part); part {
		case "nullable":
			v.Nullable = true
		case "sometimes", "":
		default:
			if v.Base == "" {
				v.Base = part
			}
		}
	}
	return v
}

// Node looks a node up by its numeric id.
func (w *Workflow) Node(id int64) *Node { return w.byID[id] }

// NodeByIndex looks a node up by "n12". Loop children are also reachable as
// "n274/lm1", since their short index is only unique inside the loop.
func (w *Workflow) NodeByIndex(index string) *Node { return w.byIndex[index] }

// Reachable returns the nodes a run can reach from the start, following
// transitions and entering loop bodies.
func (w *Workflow) Reachable() map[*Node]bool {
	seen := map[*Node]bool{}
	var visit func(*Node)
	visit = func(n *Node) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		for _, c := range n.Children {
			visit(c)
		}
		for _, t := range n.Out {
			visit(t.To)
		}
	}
	visit(w.Start)
	return seen
}

// ControlCounts tallies nodes by control, for summaries.
func (w *Workflow) ControlCounts() []struct {
	Control Control
	Count   int
} {
	m := map[Control]int{}
	for _, n := range w.Nodes {
		m[n.Control]++
	}
	out := make([]struct {
		Control Control
		Count   int
	}, 0, len(m))
	for c, k := range m {
		out = append(out, struct {
			Control Control
			Count   int
		}{c, k})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Control < out[j].Control })
	return out
}

// object decodes a JSON value that PHP may have serialised as [] or null when
// empty, returning an empty map in those cases.
func object(raw json.RawMessage) (map[string]any, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "[]" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// FormSlugs lists the form slugs a task node produces, from its output map
// (the keys that are not the task's own fields). Empty if unknown.
func (n *Node) FormSlugs() []string {
	m, ok := n.OutputMap.(map[string]any)
	if !ok || n.Control != CtrlAssignTask {
		return nil
	}
	own := map[string]bool{"state": true, "remarks": true, "metadata": true, "task_uuid": true,
		"created_at": true, "updated_at": true, "state_id": true}
	var out []string
	for k := range m {
		if !own[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
