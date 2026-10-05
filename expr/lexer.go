package expr

import (
	"fmt"
	"strings"
	"unicode"
)

type tokKind int

const (
	tEOF tokKind = iota
	tNum
	tStr
	tIdent
	tRef        // $.n13.state, bare form used in transition conditions
	tUnresolved // $.{...} still in the text after substitution
	tOp
	tLParen
	tRParen
	tComma
)

type token struct {
	kind  tokKind
	text  string
	quote byte // for strings: ' or "
	pos   int
}

// SyntaxError is a parse failure. In the live engine this fails the instance.
type SyntaxError struct {
	Pos int
	Msg string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("syntax error at %d: %s", e.Pos, e.Msg) }

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{tLParen, "(", 0, i})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, ")", 0, i})
			i++
		case c == ',':
			toks = append(toks, token{tComma, ",", 0, i})
			i++
		case c == '\'' || c == '"':
			s, end, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{tStr, s, c, i})
			i = end
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			toks = append(toks, token{tNum, src[i:j], 0, i})
			i = j
		case c == '$' && strings.HasPrefix(src[i:], "$.{"):
			end := strings.IndexByte(src[i:], '}')
			if end < 0 {
				return nil, &SyntaxError{i, "unterminated $.{ reference"}
			}
			toks = append(toks, token{tUnresolved, src[i : i+end+1], 0, i})
			i += end + 1
		case c == '$' && strings.HasPrefix(src[i:], "$."):
			j := i + 2
			for j < len(src) && isPathChar(rune(src[j])) {
				j++
			}
			path := strings.TrimRight(src[i+2:j], ".")
			if path == "" {
				return nil, &SyntaxError{i, "empty reference"}
			}
			toks = append(toks, token{tRef, path, 0, i})
			i = i + 2 + len(path)
		case isIdentStart(rune(c)):
			j := i
			for j < len(src) && (isIdentStart(rune(src[j])) || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j], 0, i})
			i = j
		default:
			op := ""
			for _, o := range []string{"==", "!=", "<=", ">=", "&&", "||", "<", ">", "+", "-", "*", "/", "!"} {
				if strings.HasPrefix(src[i:], o) {
					op = o
					break
				}
			}
			if op == "" {
				return nil, &SyntaxError{i, fmt.Sprintf("unexpected character %q", c)}
			}
			toks = append(toks, token{tOp, op, 0, i})
			i += len(op)
		}
	}
	return append(toks, token{tEOF, "", 0, len(src)}), nil
}

// lexString reads a quoted string. A backslash escapes the next character.
func lexString(src string, start int) (string, int, error) {
	q := src[start]
	var b strings.Builder
	for i := start + 1; i < len(src); i++ {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i++
				b.WriteByte(src[i])
			}
		case q:
			return b.String(), i + 1, nil
		default:
			b.WriteByte(src[i])
		}
	}
	return "", 0, &SyntaxError{start, "unterminated string"}
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }

func isPathChar(r rune) bool {
	return r == '_' || r == '-' || r == '.' || r == '*' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
