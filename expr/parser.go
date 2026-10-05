package expr

import (
	"fmt"
	"strconv"
	"strings"
)

// Node is a parsed expression.
type Node interface{ pos() int }

type (
	litNode struct {
		v Value
		p int
	}
	refNode struct { // bare $.path, transition conditions only
		path string
		p    int
	}
	unaryNode struct {
		op string
		x  Node
		p  int
	}
	binNode struct {
		op   string
		l, r Node
		p    int
	}
	callNode struct {
		name string
		args []Node
		p    int
	}
)

func (n litNode) pos() int   { return n.p }
func (n refNode) pos() int   { return n.p }
func (n unaryNode) pos() int { return n.p }
func (n binNode) pos() int   { return n.p }
func (n callNode) pos() int  { return n.p }

// Mode says which language is being parsed.
type Mode int

const (
	// ExpressionMode is Expression V2 after substitution: no references may
	// remain, and a leftover $.{...} is a syntax error.
	ExpressionMode Mode = iota
	// ConditionMode is a transition condition: bare $.n12.state references
	// are allowed and AND / OR work as well as && / ||.
	ConditionMode
)

type parser struct {
	toks []token
	i    int
	mode Mode
}

// Parse parses src. It does not substitute references; see Substitute.
func Parse(src string, mode Mode) (Node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, mode: mode}
	n, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, &SyntaxError{t.pos, fmt.Sprintf("unexpected %q", t.text)}
	}
	return n, nil
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }

// binding power of binary operators, loosest first.
func infix(t token, mode Mode) (string, int) {
	op := t.text
	if t.kind == tIdent && mode == ConditionMode {
		switch strings.ToUpper(op) {
		case "OR":
			return "||", 1
		case "AND":
			return "&&", 2
		}
	}
	if t.kind != tOp {
		return "", 0
	}
	switch op {
	case "||":
		return op, 1
	case "&&":
		return op, 2
	case "==", "!=":
		return op, 3
	case "<", ">", "<=", ">=":
		return op, 4
	case "+", "-":
		return op, 5
	case "*", "/":
		return op, 6
	}
	return "", 0
}

func (p *parser) expr(minBP int) (Node, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		op, bp := infix(t, p.mode)
		if bp == 0 || bp <= minBP {
			return left, nil
		}
		p.next()
		right, err := p.expr(bp)
		if err != nil {
			return nil, err
		}
		left = binNode{op, left, right, t.pos}
	}
}

func (p *parser) unary() (Node, error) {
	t := p.peek()
	if t.kind == tOp && (t.text == "-" || t.text == "!") {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return unaryNode{t.text, x, t.pos}, nil
	}
	return p.primary()
}

func (p *parser) primary() (Node, error) {
	t := p.next()
	switch t.kind {
	case tNum:
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, &SyntaxError{t.pos, "bad number " + t.text}
		}
		return litNode{Num(f), t.pos}, nil
	case tStr:
		return litNode{Str(t.text), t.pos}, nil
	case tRef:
		if p.mode != ConditionMode {
			return nil, &SyntaxError{t.pos, "bare $. reference is only allowed in transition conditions"}
		}
		return refNode{t.text, t.pos}, nil
	case tUnresolved:
		return nil, &SyntaxError{t.pos, "unresolved reference " + t.text}
	case tLParen:
		n, err := p.expr(0)
		if err != nil {
			return nil, err
		}
		if c := p.next(); c.kind != tRParen {
			return nil, &SyntaxError{c.pos, "expected )"}
		}
		return n, nil
	case tIdent:
		switch strings.ToLower(t.text) {
		case "true":
			return litNode{Boolean(true), t.pos}, nil
		case "false":
			return litNode{Boolean(false), t.pos}, nil
		case "null":
			return litNode{NullValue(), t.pos}, nil
		}
		if p.peek().kind != tLParen {
			return nil, &SyntaxError{t.pos, fmt.Sprintf("unknown name %q", t.text)}
		}
		p.next()
		var args []Node
		if p.peek().kind != tRParen {
			for {
				a, err := p.expr(0)
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if p.peek().kind != tComma {
					break
				}
				p.next()
			}
		}
		if c := p.next(); c.kind != tRParen {
			return nil, &SyntaxError{c.pos, fmt.Sprintf("expected ) to close %s(", t.text)}
		}
		return callNode{t.text, args, t.pos}, nil
	case tEOF:
		return nil, &SyntaxError{t.pos, "unexpected end of expression"}
	}
	return nil, &SyntaxError{t.pos, fmt.Sprintf("unexpected %q", t.text)}
}
