package config

import (
	"errors"
	"fmt"
	"strings"
)

// shellOperators are tokens that only mean something to a shell. Build
// commands run without one, so an unquoted `&&` or `|` would reach the
// program as a literal argument and fail confusingly; SplitCommand rejects
// them with a pointer to the explicit opt-in instead.
var shellOperators = map[string]bool{
	"&&": true, "||": true, "|": true, ";": true, "&": true,
	">": true, ">>": true, "<": true, "2>": true, "2>&1": true,
}

// SplitCommand splits a configured command line into an argv, without
// invoking a shell.
//
// It understands just enough quoting to write paths and arguments that
// contain spaces:
//
//   - whitespace separates arguments;
//   - 'single quotes' keep everything literal;
//   - "double quotes" keep everything literal except \" and \\;
//   - outside quotes, a backslash escapes the next character.
//
// Nothing is expanded: no $VARIABLES, globs, pipes, or redirections. A
// command that needs those must say so explicitly, e.g.
// `sh -c 'cargo build --release && wasm-opt ...'`, so a reviewer of
// soroforge.yaml can see that a shell is involved.
func SplitCommand(s string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		inArg   bool   // cur holds an argument, possibly an empty quoted one
		quoted  bool   // some part of cur came from quotes or an escape
		op      string // first unquoted shell operator seen, if any
		pending = func() {
			if !inArg {
				return
			}
			arg := cur.String()
			if !quoted && shellOperators[arg] && op == "" {
				op = arg
			}
			args = append(args, arg)
			cur.Reset()
			inArg, quoted = false, false
		}
	)

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			pending()
		case r == '\\':
			if i+1 >= len(runes) {
				return nil, errors.New("trailing backslash")
			}
			i++
			cur.WriteRune(runes[i])
			inArg, quoted = true, true
		case r == '\'':
			end := indexRune(runes, i+1, '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			cur.WriteString(string(runes[i+1 : end]))
			i = end
			inArg, quoted = true, true
		case r == '"':
			i++
			closed := false
			for ; i < len(runes); i++ {
				c := runes[i]
				if c == '"' {
					closed = true
					break
				}
				if c == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
					i++
					c = runes[i]
				}
				cur.WriteRune(c)
			}
			if !closed {
				return nil, errors.New("unterminated double quote")
			}
			inArg, quoted = true, true
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	pending()

	if len(args) == 0 {
		return nil, errors.New("command is empty")
	}
	if op != "" {
		return nil, fmt.Errorf(
			"%q is shell syntax, but build commands run without a shell; "+
				"quote it to pass it literally, or wrap the command in sh -c '...' to use a shell", op)
	}
	return args, nil
}

func indexRune(runes []rune, from int, r rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == r {
			return i
		}
	}
	return -1
}
