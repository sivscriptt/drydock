// Package expr implements OneGov's Expression V2 language and transition
// conditions, including the parts of the real engine that surprise people:
// references are pasted into the text before parsing, and how they are
// pasted is configurable (see Policy) until replay against real instances
// settles it.
package expr

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Kind int

const (
	Null Kind = iota
	Number
	String
	Bool
	List
	Map
)

func (k Kind) String() string {
	return [...]string{"null", "number", "string", "boolean", "list", "map"}[k]
}

// Value is a value in an expression or in the workflow context.
type Value struct {
	Kind Kind
	Num  float64
	Str  string
	Bool bool
	List []Value
	Map  map[string]Value
}

func Num(f float64) Value    { return Value{Kind: Number, Num: f} }
func Str(s string) Value     { return Value{Kind: String, Str: s} }
func Boolean(b bool) Value   { return Value{Kind: Bool, Bool: b} }
func NullValue() Value       { return Value{} }
func (v Value) IsNull() bool { return v.Kind == Null }

// FromAny converts decoded JSON (or plain Go values in tests) to a Value.
func FromAny(x any) Value {
	switch t := x.(type) {
	case nil:
		return Value{}
	case Value:
		return t
	case bool:
		return Boolean(t)
	case float64:
		return Num(t)
	case float32:
		return Num(float64(t))
	case int:
		return Num(float64(t))
	case int64:
		return Num(float64(t))
	case json.Number:
		f, _ := t.Float64()
		return Num(f)
	case string:
		return Str(t)
	case []any:
		l := make([]Value, len(t))
		for i, e := range t {
			l[i] = FromAny(e)
		}
		return Value{Kind: List, List: l}
	case map[string]any:
		m := make(map[string]Value, len(t))
		for k, e := range t {
			m[k] = FromAny(e)
		}
		return Value{Kind: Map, Map: m}
	}
	return Str(fmt.Sprint(x))
}

// Text renders a value the way it appears when pasted into an expression or
// shown in a field, without quotes.
func (v Value) Text() string {
	switch v.Kind {
	case Null:
		return ""
	case Number:
		return formatNum(v.Num)
	case String:
		return v.Str
	case Bool:
		return strconv.FormatBool(v.Bool)
	}
	b, _ := json.Marshal(v.toAny())
	return string(b)
}

func (v Value) String() string {
	if v.Kind == String {
		return strconv.Quote(v.Str)
	}
	if v.Kind == Null {
		return "null"
	}
	return v.Text()
}

func (v Value) toAny() any {
	switch v.Kind {
	case Number:
		return v.Num
	case String:
		return v.Str
	case Bool:
		return v.Bool
	case List:
		out := make([]any, len(v.List))
		for i, e := range v.List {
			out[i] = e.toAny()
		}
		return out
	case Map:
		out := make(map[string]any, len(v.Map))
		for k, e := range v.Map {
			out[k] = e.toAny()
		}
		return out
	}
	return nil
}

func formatNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// numeric reports whether v is a number, or a string that is one.
func (v Value) numeric() (float64, bool) {
	switch v.Kind {
	case Number:
		return v.Num, true
	case String:
		s := strings.TrimSpace(v.Str)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	case Bool:
		if v.Bool {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func (v Value) truthy() bool {
	switch v.Kind {
	case Bool:
		return v.Bool
	case Number:
		return v.Num != 0
	case String:
		return v.Str != "" && v.Str != "0" && !strings.EqualFold(v.Str, "false")
	case List:
		return len(v.List) > 0
	case Map:
		return len(v.Map) > 0
	}
	return false
}

// Get walks a dotted path ("output.0.name") into lists and maps.
func (v Value) Get(path []string) (Value, bool) {
	cur := v
	for _, p := range path {
		switch cur.Kind {
		case Map:
			next, ok := cur.Map[p]
			if !ok {
				return Value{}, false
			}
			cur = next
		case List:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(cur.List) {
				return Value{}, false
			}
			cur = cur.List[i]
		default:
			return Value{}, false
		}
	}
	return cur, true
}
