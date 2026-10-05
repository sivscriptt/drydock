package workflow

import (
	"fmt"
	"strings"
)

type Severity int

const (
	Info Severity = iota
	Warning
	Error
)

func (s Severity) String() string {
	return [...]string{"info", "warning", "error"}[s]
}

// Problem is something wrong or suspicious in the graph itself. Problems
// that need a run to find (a reference missing on one path, a loop that
// stalls) come from the simulator, not from here.
type Problem struct {
	Severity Severity
	Code     string
	Node     string // node index, if the problem is about one node
	Message  string
}

func (p Problem) String() string {
	if p.Node != "" {
		return fmt.Sprintf("%-7s %-26s %-8s %s", p.Severity, p.Code, p.Node, p.Message)
	}
	return fmt.Sprintf("%-7s %-26s %-8s %s", p.Severity, p.Code, "", p.Message)
}

// Check finds structural problems: nodes no run can reach, paths that stop
// at a node that is not an end, and nodes whose way out is ambiguous.
func Check(w *Workflow) []Problem {
	var out []Problem
	reach := w.Reachable()

	for _, n := range w.Nodes {
		if !reach[n] {
			out = append(out, Problem{Warning, "unreachable", n.Index, fmt.Sprintf("%s can never run", n.Name)})
		}
		if n.Loop != nil {
			continue // loop children have no transitions by design
		}
		if len(n.Out) == 0 && !n.Control.Terminal() && reach[n] {
			out = append(out, Problem{Warning, "dead-end", n.Index,
				fmt.Sprintf("%s (%s) has no way out, so a case that reaches it stops here without ending", n.Name, n.Control)})
		}

		var always []*Transition
		for _, t := range n.Out {
			if t.Condition == "" {
				always = append(always, t)
			}
		}
		if len(always) > 1 {
			targets := make([]string, len(always))
			for i, t := range always {
				targets[i] = t.To.Index
			}
			out = append(out, Problem{Warning, "multiple-unconditional", n.Index,
				fmt.Sprintf("%d transitions with no condition (to %s); whether the engine forks or takes one is not settled",
					len(always), strings.Join(targets, ", "))})
		}
		if len(always) == 1 && len(n.Out) > 1 {
			out = append(out, Problem{Info, "default-with-conditions", n.Index,
				fmt.Sprintf("mixes conditional transitions with an unconditional one to %s", always[0].To.Index)})
		}
	}
	return out
}
