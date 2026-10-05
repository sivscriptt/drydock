package expr

import (
	"fmt"
	"strings"
	"time"
)

// Context resolves references. Paths are dotted, without the "$." prefix:
// "applicantID", "n9.output.total", "n20.form-slug.field".
type Context interface {
	Lookup(path string) (Value, bool)
}

// MapContext is a Context over a plain map, for tests and fixtures.
type MapContext map[string]any

func (m MapContext) Lookup(path string) (Value, bool) {
	parts := strings.Split(path, ".")
	root, ok := m[parts[0]]
	if !ok {
		return Value{}, false
	}
	return FromAny(root).Get(parts[1:])
}

// EvalError is a runtime failure: wrong types, an unknown function, a
// division by zero.
type EvalError struct {
	Pos int
	Msg string
}

func (e *EvalError) Error() string { return fmt.Sprintf("evaluation error at %d: %s", e.Pos, e.Msg) }

type evaluator struct {
	ctx    Context
	policy Policy
	now    time.Time
	// unresolved collects condition references that had no value.
	unresolved []string
}

func (ev *evaluator) eval(n Node) (Value, error) {
	switch n := n.(type) {
	case litNode:
		return n.v, nil
	case refNode:
		if ev.ctx != nil {
			if v, ok := ev.ctx.Lookup(n.path); ok {
				return v, nil
			}
		}
		ev.unresolved = append(ev.unresolved, n.path)
		return NullValue(), nil
	case unaryNode:
		x, err := ev.eval(n.x)
		if err != nil {
			return x, err
		}
		if n.op == "!" {
			return Boolean(!x.truthy()), nil
		}
		f, ok := x.numeric()
		if !ok {
			return x, &EvalError{n.p, fmt.Sprintf("cannot negate %s", x.Kind)}
		}
		return Num(-f), nil
	case binNode:
		return ev.binary(n)
	case callNode:
		return ev.call(n)
	}
	return Value{}, fmt.Errorf("unknown node %T", n)
}

func (ev *evaluator) binary(n binNode) (Value, error) {
	// && and || short-circuit at evaluation time. (Parsing has already
	// happened for both sides; that is where the engine's real trap is.)
	if n.op == "&&" || n.op == "||" {
		l, err := ev.eval(n.l)
		if err != nil {
			return l, err
		}
		if n.op == "&&" && !l.truthy() {
			return Boolean(false), nil
		}
		if n.op == "||" && l.truthy() {
			return Boolean(true), nil
		}
		r, err := ev.eval(n.r)
		if err != nil {
			return r, err
		}
		return Boolean(r.truthy()), nil
	}

	l, err := ev.eval(n.l)
	if err != nil {
		return l, err
	}
	r, err := ev.eval(n.r)
	if err != nil {
		return r, err
	}
	switch n.op {
	case "==":
		return Boolean(ev.equal(l, r)), nil
	case "!=":
		return Boolean(!ev.equal(l, r)), nil
	case "<", ">", "<=", ">=":
		c, ok := ev.compare(l, r)
		if !ok {
			return Value{}, &EvalError{n.p, fmt.Sprintf("cannot compare %s with %s", l.Kind, r.Kind)}
		}
		switch n.op {
		case "<":
			return Boolean(c < 0), nil
		case ">":
			return Boolean(c > 0), nil
		case "<=":
			return Boolean(c <= 0), nil
		}
		return Boolean(c >= 0), nil
	}

	// Arithmetic.
	a, okA := l.numeric()
	b, okB := r.numeric()
	if !okA || !okB {
		if n.op == "+" && (l.Kind == String || r.Kind == String) {
			return Value{}, &EvalError{n.p, "+ does not join text in Expression V2; put the references inside one double-quoted string"}
		}
		return Value{}, &EvalError{n.p, fmt.Sprintf("%s needs numbers, got %s and %s", n.op, l.Kind, r.Kind)}
	}
	switch n.op {
	case "+":
		return Num(a + b), nil
	case "-":
		return Num(a - b), nil
	case "*":
		return Num(a * b), nil
	case "/":
		if b == 0 {
			return Value{}, &EvalError{n.p, "division by zero"}
		}
		return Num(a / b), nil
	}
	return Value{}, &EvalError{n.p, "unknown operator " + n.op}
}

// equal follows the policy: strict (same type only, as the function docs
// say for ==) or loose (a number equals the string spelling of it, which is
// what transition conditions like $.n3.count != "0" rely on).
func (ev *evaluator) equal(a, b Value) bool {
	if a.Kind == b.Kind {
		switch a.Kind {
		case Null:
			return true
		case Number:
			return a.Num == b.Num
		case String:
			return a.Str == b.Str
		case Bool:
			return a.Bool == b.Bool
		}
		return a.Text() == b.Text()
	}
	if !ev.policy.LooseEquality {
		return false
	}
	// Null equals the empty string, the engine's blank.
	if a.Kind == Null || b.Kind == Null {
		return a.Text() == "" && b.Text() == ""
	}
	if x, ok := a.numeric(); ok {
		if y, ok := b.numeric(); ok {
			return x == y
		}
	}
	return a.Text() == b.Text()
}

func (ev *evaluator) compare(a, b Value) (int, bool) {
	x, okA := a.numeric()
	y, okB := b.numeric()
	if okA && okB && (a.Kind == Number || b.Kind == Number || ev.policy.LooseEquality) {
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
		return 0, true
	}
	if a.Kind == String && b.Kind == String {
		return strings.Compare(a.Str, b.Str), true
	}
	return 0, false
}

// Eval parses and evaluates a source that needs no substitution.
func Eval(src string, mode Mode, ctx Context, policy Policy) (Value, error) {
	n, err := Parse(src, mode)
	if err != nil {
		return Value{}, err
	}
	ev := &evaluator{ctx: ctx, policy: policy, now: policy.clock()}
	return ev.eval(n)
}
