package sim

import (
	"strconv"
	"strings"

	"github.com/sivscriptt/drydock/expr"
	"github.com/sivscriptt/drydock/workflow"
)

// scope is the instance context: workflow variables, "meta", and each node's
// output under its index, like context_data on a real instance.
type scope struct {
	data  map[string]any
	loops []*loopFrame // innermost last
}

type loopFrame struct {
	node  *workflow.Node
	index int
	// out[i] holds the outputs of iteration i, keyed by child index ("l1").
	out []any
}

// Lookup resolves a reference path. "currentIndex" means the iteration the
// innermost loop is on. A loop's own output is visible while it runs, which
// is how a child reads an earlier sibling: $.{n9.output.currentIndex.l1.x}.
// A bare child index ($.{l1.x}) is not in the context, so it resolves to
// nothing, as on the platform.
func (s *scope) Lookup(path string) (expr.Value, bool) {
	parts := strings.Split(path, ".")
	if len(s.loops) > 0 {
		cur := strconv.Itoa(s.loops[len(s.loops)-1].index)
		for i, p := range parts {
			if p == "currentIndex" {
				parts[i] = cur
			}
		}
	}
	root := parts[0]
	for i := len(s.loops) - 1; i >= 0; i-- {
		if f := s.loops[i]; f.node.Index == root {
			v := expr.FromAny(map[string]any{"output": f.out})
			return v.Get(parts[1:])
		}
	}
	v, ok := s.data[root]
	if !ok {
		return expr.Value{}, false
	}
	return expr.FromAny(v).Get(parts[1:])
}

func (s *scope) set(key string, v any) { s.data[key] = v }
