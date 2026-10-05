// Package viewer renders a workflow and its explored scenarios as one
// self-contained HTML page that plays each scenario on the diagram.
package viewer

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"regexp"
	"strings"

	"github.com/sivscriptt/drydock/explore"
	"github.com/sivscriptt/drydock/sim"
	"github.com/sivscriptt/drydock/workflow"
)

var (
	//go:embed assets/dagre.min.js
	dagreJS string
	//go:embed assets/view.css
	viewCSS string
	//go:embed assets/view.js
	viewJS string
	//go:embed assets/view.html
	pageHTML string
)

// Model is what the page draws and plays.
type Model struct {
	Workflow  string     `json:"workflow"`
	Version   string     `json:"version"`
	Nodes     []Node     `json:"nodes"`
	Edges     []Edge     `json:"edges"`
	Scenarios []Scenario `json:"scenarios"`
	Findings  []Finding  `json:"findings"`
	Coverage  Coverage   `json:"coverage"`
	Mode      string     `json:"mode"`
	Note      string     `json:"note,omitempty"`
}

type Node struct {
	ID        string   `json:"id"` // node index, "n14"
	Name      string   `json:"name"`
	Kind      string   `json:"kind"` // start, end, task, lookup, write, expression, loop, message, document, payment, other
	Control   string   `json:"control"`
	Milestone string   `json:"milestone,omitempty"`
	Children  []string `json:"children,omitempty"` // loop body, in run order
}

type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
	Label     string `json:"label,omitempty"` // short form for the diagram
	Status    string `json:"status,omitempty"`
}

type Scenario struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Title     string   `json:"title"` // one line, plain words
	Decisions []string `json:"decisions"`
	FlowState string   `json:"flowState,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	At        string   `json:"at,omitempty"`
	Steps     []Step   `json:"steps"`
}

type Step struct {
	Node   string `json:"node"`
	Event  string `json:"event"`
	Detail string `json:"detail,omitempty"`
	Next   string `json:"next,omitempty"`   // the transition taken
	Status string `json:"status,omitempty"` // public status set on that transition
	Loop   int    `json:"loop,omitempty"`   // loop iterations
}

type Finding struct {
	Status    string `json:"status"`
	At        string `json:"at"`
	Reason    string `json:"reason"`
	Scenarios int    `json:"scenarios"`
	Example   string `json:"example"`
}

type Coverage struct {
	Nodes      int            `json:"nodes"`
	NodesHit   int            `json:"nodesHit"`
	Edges      int            `json:"edges"`
	EdgesTaken int            `json:"edgesTaken"`
	Visits     map[string]int `json:"visits"` // scenarios passing each node
	Untaken    []string       `json:"untaken"`
}

func kindOf(c workflow.Control, n *workflow.Node, start bool) string {
	switch {
	case start:
		return "start"
	case c.Terminal():
		return "end"
	}
	switch c {
	case workflow.CtrlAssignTask:
		return "task"
	case workflow.CtrlDataHub:
		if op, _ := n.Params["operation"].(string); strings.EqualFold(op, "select") {
			return "lookup"
		}
		return "write"
	case workflow.CtrlExpressionV2, workflow.CtrlExpressionV1:
		return "expression"
	case workflow.CtrlLoop:
		return "loop"
	case workflow.CtrlSMS, workflow.CtrlEmail, workflow.CtrlNotes, workflow.CtrlInfo:
		return "message"
	case workflow.CtrlGenerateFile, workflow.CtrlGenerateFile2, workflow.CtrlDCN, workflow.CtrlEDMS, workflow.CtrlLocker:
		return "document"
	case workflow.CtrlInvoice, workflow.CtrlPaymentStatus:
		return "payment"
	case workflow.CtrlNoAction:
		return "route"
	}
	return "other"
}

// Build turns a workflow and an exploration into the page model.
func Build(w *workflow.Workflow, res *explore.Result) *Model {
	m := &Model{Workflow: w.Name, Version: w.Version, Mode: string(res.Mode), Note: res.Fallback}
	milestones := map[int64]string{}
	for _, ms := range w.Milestones {
		milestones[ms.ID] = ms.Name
	}
	reach := w.Reachable()
	phase := phases(w, milestones)
	for _, n := range w.Nodes {
		if n.Loop != nil || !reach[n] {
			continue
		}
		nd := Node{ID: n.Index, Name: displayName(n.Name), Kind: kindOf(n.Control, n, n == w.Start), Control: n.Control.String()}
		nd.Milestone = phase[n]
		for _, c := range n.Children {
			nd.Children = append(nd.Children, c.Index+" · "+displayName(c.Name))
		}
		m.Nodes = append(m.Nodes, nd)
		for _, t := range n.Out {
			e := Edge{From: n.Index, To: t.To.Index, Condition: t.Condition, Label: shortCondition(t.Condition)}
			if t.StateChange != nil {
				if fs, ok := w.FlowStates[*t.StateChange]; ok {
					e.Status = fs.Label
					if fs.PublicLabel != "" {
						e.Status = fs.PublicLabel
					}
				}
			}
			m.Edges = append(m.Edges, e)
		}
	}

	visits := map[string]int{}
	for _, sc := range res.Scenarios {
		s := Scenario{ID: sc.ID, Status: string(sc.Status), Decisions: sc.Decisions, FlowState: sc.FlowState,
			Reason: firstLine(sc.Reason), At: sc.At}
		s.Title = title(sc)
		seen := map[string]bool{}
		for _, st := range sc.Steps() {
			step := Step{Node: st.Node, Event: st.Event, Detail: clip(st.Detail, 220), Loop: iterations(st)}
			for _, e := range st.Edges {
				if e.Taken {
					step.Next, step.Status = e.To, e.StateChange
					break
				}
			}
			s.Steps = append(s.Steps, step)
			if !seen[st.Node] {
				seen[st.Node] = true
				visits[st.Node]++
			}
		}
		m.Scenarios = append(m.Scenarios, s)
	}
	for _, f := range res.Findings {
		m.Findings = append(m.Findings, Finding{Status: string(f.Status), At: f.At, Reason: f.Reason, Scenarios: f.Scenarios, Example: f.Example.ID})
	}
	c := res.Coverage
	m.Coverage = Coverage{Nodes: c.Nodes, NodesHit: c.NodesHit, Edges: c.Edges, EdgesTaken: c.EdgesTaken, Visits: visits}
	for _, u := range c.Untaken {
		// "n13 → n129  when …" -> "n13>n129" so the page can find the edge.
		parts := strings.SplitN(u, "  when ", 2)
		m.Coverage.Untaken = append(m.Coverage.Untaken, strings.ReplaceAll(strings.ReplaceAll(parts[0], " → ", ">"), " ", ""))
	}
	return m
}

func iterations(st sim.Step) int {
	if len(st.Children) == 0 {
		return 0
	}
	items := map[string]bool{}
	for _, c := range st.Children {
		if i := strings.Index(c.Detail, "]"); strings.HasPrefix(c.Detail, "[item ") && i > 0 {
			items[c.Detail[:i]] = true
		}
	}
	return len(items)
}

// title is one plain line saying how the scenario ends.
func title(sc *explore.Scenario) string {
	switch sc.Status {
	case sim.Completed:
		if sc.FlowState != "" {
			return sc.FlowState
		}
		return "Completed"
	case sim.Failed:
		return "Fails at " + sc.At
	case sim.Stuck:
		return "Stuck at " + sc.At
	case sim.Waiting:
		if sc.Pruned != "" {
			return "Not explored further"
		}
		return "Waiting at " + sc.At
	}
	return string(sc.Status)
}

var condRepl = strings.NewReplacer(`$.`, ``, ` AND `, ` and `, ` OR `, ` or `, `&&`, `and`, `||`, `or`)

// shortCondition turns `$.n14.state == "Approved"` into `state = Approved`
// for the diagram; the full condition is shown on hover.
func shortCondition(c string) string {
	if c == "" {
		return ""
	}
	s := condRepl.Replace(c)
	parts := strings.Fields(s)
	for i, p := range parts {
		// Drop the node and form-slug prefix: n14.form-slug.field -> field
		if strings.Count(p, ".") >= 1 && len(p) > 1 && p[0] == 'n' {
			segs := strings.Split(p, ".")
			parts[i] = segs[len(segs)-1]
		}
	}
	s = strings.Join(parts, " ")
	s = strings.ReplaceAll(s, " == ", " = ")
	s = strings.ReplaceAll(s, `"`, "")
	return clip(s, 48)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

// Render writes the page.
func Render(out io.Writer, m *Model) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	t, err := template.New("page").Parse(pageHTML)
	if err != nil {
		return fmt.Errorf("viewer template: %w", err)
	}
	return t.Execute(out, map[string]any{
		"Title": m.Workflow + " v" + m.Version,
		"CSS":   template.CSS(viewCSS),
		"Dagre": template.JS(dagreJS),
		"App":   template.JS(viewJS),
		// Inside <script type="application/json">; html/template escapes </.
		"Data": template.JS(strings.ReplaceAll(string(data), "</", `<\/`)),
	})
}

// phases gives every node the milestone it belongs to. Only tasks carry a
// milestone in the export, so each node inherits the milestone of the last
// task before it, walking forward from the start.
func phases(w *workflow.Workflow, names map[int64]string) map[*workflow.Node]string {
	out := map[*workflow.Node]string{}
	type item struct {
		n  *workflow.Node
		ms string
	}
	queue := []item{{w.Start, ""}}
	seen := map[*workflow.Node]bool{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.n == nil || seen[it.n] {
			continue
		}
		seen[it.n] = true
		ms := it.ms
		if it.n.Milestone != nil && names[*it.n.Milestone] != "" {
			ms = names[*it.n.Milestone]
		}
		out[it.n] = ms
		for _, t := range it.n.Out {
			queue = append(queue, item{t.To, ms})
		}
	}
	return out
}

var (
	parenRef = regexp.MustCompile(`\(\s*\$\.\{[^}]*\}\s*\)`)
	bareRef  = regexp.MustCompile(`\$\.\{[^}]*\}`)
	dashes   = regexp.MustCompile(`(\s*-\s*){2,}`)
)

// displayName drops the per-case placeholders task titles carry
// ("Verify application ( $.{applicantID} )" -> "Verify application"): on a
// diagram of the workflow, not of one case, they are noise.
func displayName(name string) string {
	s := parenRef.ReplaceAllString(name, "")
	s = bareRef.ReplaceAllString(s, "")
	s = dashes.ReplaceAllString(s, " - ")
	s = strings.Trim(strings.Join(strings.Fields(s), " "), " -:·")
	if s == "" {
		return name
	}
	return s
}
