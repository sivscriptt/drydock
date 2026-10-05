package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Load reads a workflow from either format govcraft writes:
//
//   - an export file: one JSON object with workflow, nodes, transitions,
//     variables, milestones, triggerMap (and optionally flowStates);
//   - a review bundle folder: <name>.record.json, <name>.nodes.json,
//     <name>.transitions.json and friends, as written by the review archiver.
//     Only this format includes loop children.
//
// Load returns the problems it found alongside the workflow. A problem does
// not stop loading unless the graph cannot be built at all.
func Load(path string) (*Workflow, []Problem, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	var raw rawExport
	if info.IsDir() {
		raw, err = readBundle(path)
	} else {
		raw, err = readExport(path)
	}
	if err != nil {
		return nil, nil, err
	}
	return build(raw)
}

type rawExport struct {
	Workflow    rawWorkflow       `json:"workflow"`
	Nodes       []rawNode         `json:"nodes"`
	Transitions []rawTransition   `json:"transitions"`
	Variables   map[string]string `json:"variables"`
	Milestones  []rawMilestone    `json:"milestones"`
	FlowStates  []rawFlowState    `json:"flowStates"`
	TriggerMap  json.RawMessage   `json:"triggerMap"`
}

type rawWorkflow struct {
	ID            int64           `json:"id"`
	UUID          string          `json:"uuid"`
	Name          string          `json:"name"`
	Version       json.RawMessage `json:"version"`
	Specification struct {
		Trigger json.RawMessage `json:"trigger"`
	} `json:"specification"`
}

type rawNode struct {
	ID                int64           `json:"id"`
	UUID              string          `json:"uuid"`
	ParentID          *int64          `json:"parent_id"`
	NodeIndex         string          `json:"node_index"`
	WorkflowControlID int             `json:"workflow_control_id"`
	Milestone         *int64          `json:"milestone"`
	ParameterMap      json.RawMessage `json:"parameter_map"`
	OutputMap         json.RawMessage `json:"output_map"`
	Metadata          json.RawMessage `json:"metadata"`
}

type rawTransition struct {
	ID          int64           `json:"id"`
	From        int64           `json:"from_node_id"`
	To          int64           `json:"to_node_id"`
	Conditions  *string         `json:"conditions"`
	StateChange *int64          `json:"state_change"`
	Active      json.RawMessage `json:"active"`
}

type rawMilestone struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}

type rawFlowState struct {
	ID          int64   `json:"id"`
	Label       string  `json:"label"`
	PublicLabel *string `json:"public_label"`
}

// rawCompact is the short-key snapshot some review scripts write. It is the
// only single-file format that carries loop children, keyed by loop index.
type rawCompact struct {
	Rec        rawWorkflow          `json:"rec"`
	Nodes      []rawNode            `json:"nodes"`
	Children   map[string][]rawNode `json:"children"`
	Trans      []rawTransition      `json:"trans"`
	TM         json.RawMessage      `json:"tm"`
	MS         []rawMilestone       `json:"ms"`
	FlowStates []rawFlowState       `json:"flowStates"`
	Variables  map[string]string    `json:"variables"`
}

func readExport(path string) (rawExport, error) {
	var raw rawExport
	b, err := os.ReadFile(path)
	if err != nil {
		return raw, err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return raw, fmt.Errorf("%s: %w", path, err)
	}

	switch {
	case keys["transitions"] != nil:
		if err := json.Unmarshal(b, &raw); err != nil {
			return raw, fmt.Errorf("%s: %w", path, err)
		}
	case keys["trans"] != nil && keys["rec"] != nil:
		var c rawCompact
		if err := json.Unmarshal(b, &c); err != nil {
			return raw, fmt.Errorf("%s: %w", path, err)
		}
		raw = rawExport{Workflow: c.Rec, Nodes: c.Nodes, Transitions: c.Trans, Variables: c.Variables,
			Milestones: c.MS, FlowStates: c.FlowStates, TriggerMap: c.TM}
		loops := make([]string, 0, len(c.Children))
		for k := range c.Children {
			loops = append(loops, k)
		}
		sort.Strings(loops)
		for _, k := range loops {
			raw.Nodes = append(raw.Nodes, c.Children[k]...)
		}
	default:
		// Refuse rather than half-load: a graph with no transitions would
		// simulate as a workflow that ends at its start node.
		return raw, fmt.Errorf("%s: not a workflow export I recognise (no transitions)", path)
	}
	if len(raw.Nodes) == 0 {
		return raw, fmt.Errorf("%s: no nodes; is this a govcraft workflow export?", path)
	}
	return raw, nil
}

// readBundle reads a review bundle folder. It accepts the raw/workflows
// folder itself, or any parent of it.
func readBundle(dir string) (rawExport, error) {
	var raw rawExport
	matches, _ := filepath.Glob(filepath.Join(dir, "*.nodes.json"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(dir, "raw", "workflows", "*.nodes.json"))
	}
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(dir, "workflows", "*.nodes.json"))
	}
	if len(matches) != 1 {
		return raw, fmt.Errorf("%s: expected one *.nodes.json in the bundle, found %d", dir, len(matches))
	}
	prefix := strings.TrimSuffix(matches[0], ".nodes.json")

	parts := []struct {
		suffix   string
		into     any
		required bool
	}{
		{".record.json", &raw.Workflow, true},
		{".nodes.json", &raw.Nodes, true},
		{".transitions.json", &raw.Transitions, true},
		{".variables.json", &raw.Variables, false},
		{".milestones.json", &raw.Milestones, false},
		{".flowstates.json", &raw.FlowStates, false},
		{".triggermap.json", &raw.TriggerMap, false},
	}
	for _, p := range parts {
		b, err := os.ReadFile(prefix + p.suffix)
		if errors.Is(err, os.ErrNotExist) && !p.required {
			continue
		}
		if err != nil {
			return raw, err
		}
		if err := json.Unmarshal(b, p.into); err != nil {
			return raw, fmt.Errorf("%s%s: %w", prefix, p.suffix, err)
		}
	}
	return raw, nil
}

func build(raw rawExport) (*Workflow, []Problem, error) {
	w := &Workflow{
		ID:         raw.Workflow.ID,
		UUID:       raw.Workflow.UUID,
		Name:       raw.Workflow.Name,
		Version:    strings.Trim(string(raw.Workflow.Version), `"`),
		Variables:  map[string]VarType{},
		FlowStates: map[int64]FlowState{},
		byID:       map[int64]*Node{},
		byIndex:    map[string]*Node{},
	}
	var problems []Problem

	trigger := raw.TriggerMap
	if len(trigger) == 0 || string(trigger) == "null" || string(trigger) == "[]" {
		trigger = raw.Workflow.Specification.Trigger
	}
	w.Trigger = parseTrigger(trigger)
	for name, t := range raw.Variables {
		w.Variables[name] = parseVarType(t)
	}
	// Some exports carry no variable list. Every trigger field becomes a
	// variable on the platform, so take them from the trigger map, type
	// unknown.
	if len(w.Variables) == 0 {
		for name := range w.Trigger {
			w.Variables[name] = VarType{Nullable: true}
		}
	}
	for _, m := range raw.Milestones {
		w.Milestones = append(w.Milestones, Milestone{ID: m.ID, Name: m.Name, Order: m.Order})
	}
	sort.Slice(w.Milestones, func(i, j int) bool { return w.Milestones[i].Order < w.Milestones[j].Order })
	for _, f := range raw.FlowStates {
		fs := FlowState{ID: f.ID, Label: f.Label}
		if f.PublicLabel != nil {
			fs.PublicLabel = *f.PublicLabel
		}
		w.FlowStates[f.ID] = fs
	}

	// Nodes.
	parentOf := map[*Node]int64{}
	for _, rn := range raw.Nodes {
		params, err := object(rn.ParameterMap)
		if err != nil {
			return nil, nil, fmt.Errorf("node %s parameter_map: %w", rn.NodeIndex, err)
		}
		var out any
		if len(rn.OutputMap) > 0 {
			json.Unmarshal(rn.OutputMap, &out)
		}
		meta, _ := object(rn.Metadata)
		n := &Node{
			ID: rn.ID, UUID: rn.UUID, Index: rn.NodeIndex, Control: Control(rn.WorkflowControlID),
			Milestone: rn.Milestone, Params: params, OutputMap: out,
		}
		n.Name = nodeName(n, meta)
		if _, dup := w.byID[n.ID]; dup {
			problems = append(problems, Problem{Error, "duplicate-node", n.Index, fmt.Sprintf("node id %d appears twice; keeping the first", n.ID)})
			continue
		}
		w.byID[n.ID] = n
		w.Nodes = append(w.Nodes, n)
		if rn.ParentID != nil {
			parentOf[n] = *rn.ParentID
		}
		if w.Start == nil && n.Control == CtrlNoAction && str(meta["type"]) == "start" {
			w.Start = n
		}
	}

	// Loop bodies: children run in the loop's "actions" order.
	for _, n := range w.Nodes {
		if n.Control != CtrlLoop {
			continue
		}
		for _, id := range int64s(n.Params["actions"]) {
			c := w.byID[id]
			if c == nil {
				problems = append(problems, Problem{Error, "missing-loop-child", n.Index,
					fmt.Sprintf("loop child %d is not in the export. Plain exports leave loop children out; export a review bundle, or fetch them with get_workflow_nodes(parent_id=%d)", id, n.ID)})
				continue
			}
			c.Loop = n
			n.Children = append(n.Children, c)
		}
	}
	for n, pid := range parentOf {
		if n.Loop == nil {
			if p := w.byID[pid]; p != nil && p.Control == CtrlLoop {
				problems = append(problems, Problem{Warning, "loop-child-not-in-actions", n.Index,
					fmt.Sprintf("has parent loop %s but is not in its actions list, so it never runs", p.Index)})
			}
		}
	}

	// Index lookup. Loop child indexes ("l1", "lm1") are only unique within
	// their loop, so they are also registered as "n274/lm1".
	for _, n := range w.Nodes {
		key := n.Index
		if n.Loop != nil {
			w.byIndex[n.Loop.Index+"/"+n.Index] = n
			if _, taken := w.byIndex[key]; taken {
				continue
			}
		}
		if prev, taken := w.byIndex[key]; taken && prev.Loop == nil && n.Loop == nil {
			problems = append(problems, Problem{Warning, "duplicate-index", n.Index, "two nodes share this index"})
			continue
		}
		w.byIndex[key] = n
	}
	sort.SliceStable(w.Nodes, func(i, j int) bool { return indexLess(w.Nodes[i], w.Nodes[j]) })

	// Transitions.
	inactive := 0
	for _, rt := range raw.Transitions {
		if !truthy(rt.Active) {
			inactive++
			continue
		}
		from, to := w.byID[rt.From], w.byID[rt.To]
		if from == nil || to == nil {
			problems = append(problems, Problem{Error, "dangling-transition", "",
				fmt.Sprintf("transition %d joins node %d to node %d, but one of them is not in the workflow", rt.ID, rt.From, rt.To)})
			continue
		}
		t := &Transition{ID: rt.ID, From: from, To: to, StateChange: rt.StateChange}
		if rt.Conditions != nil {
			t.Condition = strings.TrimSpace(*rt.Conditions)
		}
		from.Out = append(from.Out, t)
		to.In = append(to.In, t)
		w.Transitions = append(w.Transitions, t)
	}
	if inactive > 0 {
		problems = append(problems, Problem{Info, "inactive-transitions", "", fmt.Sprintf("%d inactive transitions ignored", inactive)})
	}

	if w.Start == nil {
		// Older exports lack metadata.type; fall back to a no-action node
		// that nothing points at.
		for _, n := range w.Nodes {
			if n.Control == CtrlNoAction && len(n.In) == 0 && n.Loop == nil && len(n.Out) > 0 {
				w.Start = n
				break
			}
		}
	}
	if w.Start == nil {
		return w, append(problems, Problem{Error, "no-start", "", "no start node found"}), errors.New("no start node found")
	}
	return w, append(problems, Check(w)...), nil
}

func parseTrigger(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	for k, v := range m {
		var withValue struct {
			Value string `json:"value"`
		}
		var plain string
		switch {
		case json.Unmarshal(v, &withValue) == nil && withValue.Value != "":
			out[k] = withValue.Value
		case json.Unmarshal(v, &plain) == nil:
			out[k] = plain
		}
	}
	return out
}

func nodeName(n *Node, meta map[string]any) string {
	for _, s := range []string{str(meta["label"]), str(n.Params["title"]), str(meta["description"])} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return n.Control.String()
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func int64s(v any) []int64 {
	arr, _ := v.([]any)
	out := make([]int64, 0, len(arr))
	for _, x := range arr {
		switch t := x.(type) {
		case float64:
			out = append(out, int64(t))
		case string:
			if i, err := strconv.ParseInt(t, 10, 64); err == nil {
				out = append(out, i)
			}
		}
	}
	return out
}

func truthy(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "0", "false", `"0"`:
		return false
	}
	return true // missing means active
}

// indexLess orders n2 before n10, and loop children after their loop.
func indexLess(a, b *Node) bool {
	ka, kb := sortKey(a), sortKey(b)
	if ka[0] != kb[0] {
		return ka[0] < kb[0]
	}
	if ka[1] != kb[1] {
		return ka[1] < kb[1]
	}
	return a.Index < b.Index
}

func sortKey(n *Node) [2]int {
	if n.Loop != nil {
		return [2]int{num(n.Loop.Index), 1 + childPos(n)}
	}
	return [2]int{num(n.Index), 0}
}

func childPos(n *Node) int {
	for i, c := range n.Loop.Children {
		if c == n {
			return i
		}
	}
	return 0
}

func num(index string) int {
	i, err := strconv.Atoi(strings.TrimLeft(index, "nlm"))
	if err != nil {
		return 1 << 30
	}
	return i
}
