package agentproto

import (
	"errors"
	"strings"
)

// The SQL console hands the statement to the psql / mysql client on stdin, inside a transaction the
// agent opens and closes itself. Two things must never get through with it:
//
//   - a second statement: "SELECT 1; COMMIT; DROP TABLE t" would end the read-only transaction and
//     run the DROP outside it (and outside the audit a write gets);
//   - a client command: psql's \! and mysql's \! / system run a shell command in the container,
//     which is not SQL at all.
//
// So the text is lexed the way the client lexes it (quotes, comments, dollar quotes) and refused
// unless it is exactly one statement with no backslash outside a literal. It is checked on both
// sides of the wire, like every other param.

var errOneStatement = errors.New("run one statement at a time (no ';' followed by more SQL)")
var errClientCommand = errors.New("client commands (backslash commands, system, source, …) are not allowed")

// mysqlClientCommands are the mysql client's named commands, recognised as the first word of the input.
var mysqlClientCommands = map[string]bool{
	"help": true, "clear": true, "connect": true, "delimiter": true, "edit": true, "ego": true, "exit": true,
	"go": true, "nopager": true, "notee": true, "pager": true, "print": true, "prompt": true, "quit": true,
	"rehash": true, "source": true, "status": true, "system": true, "tee": true, "use": true, "charset": true,
	"warnings": true, "nowarning": true, "resetconnection": true, "query_attributes": true,
	"ssl_session_data_print": true,
}

// SingleSQLStatement returns the statement with any trailing ';' and whitespace removed, or an error
// when sql holds more than one statement or a client command. kind is "postgres" or "mysql".
func SingleSQLStatement(kind, sql string) (string, error) {
	pg := kind == "postgres"
	s := sql
	end := -1 // index of the terminating top-level ';'
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case end >= 0:
			// Only whitespace and comments may follow the terminating ';'.
			switch {
			case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ';':
				i++
			case isLineComment(s, i, pg):
				i = skipLine(s, i)
			case strings.HasPrefix(s[i:], "/*"):
				j, err := skipBlockComment(s, i, pg)
				if err != nil {
					return "", err
				}
				i = j
			default:
				return "", errOneStatement
			}
			continue
		case c == '\\':
			return "", errClientCommand
		case c == ';':
			end = i
			i++
		case isLineComment(s, i, pg):
			i = skipLine(s, i)
		case strings.HasPrefix(s[i:], "/*"):
			j, err := skipBlockComment(s, i, pg)
			if err != nil {
				return "", err
			}
			i = j
		case c == '\'':
			backslashEscapes := !pg || (i > 0 && (s[i-1] == 'e' || s[i-1] == 'E') && (i < 2 || !isIdentByte(s[i-2])))
			j, ok := skipQuoted(s, i, '\'', backslashEscapes)
			if !ok {
				return "", errors.New("unterminated string literal")
			}
			i = j
		case c == '"':
			j, ok := skipQuoted(s, i, '"', !pg)
			if !ok {
				return "", errors.New("unterminated quoted identifier")
			}
			i = j
		case c == '`' && !pg:
			j, ok := skipQuoted(s, i, '`', false)
			if !ok {
				return "", errors.New("unterminated quoted identifier")
			}
			i = j
		case c == '$' && pg && (i == 0 || !isIdentByte(s[i-1])):
			tag, ok := dollarTag(s, i)
			if !ok {
				i++
				continue
			}
			k := strings.Index(s[i+len(tag):], tag)
			if k < 0 {
				return "", errors.New("unterminated dollar-quoted string")
			}
			i += len(tag) + k + len(tag)
		default:
			i++
		}
	}
	stmt := s
	if end >= 0 {
		stmt = s[:end]
	}
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return "", errors.New("sql is required")
	}
	if !pg {
		first := strings.ToLower(strings.FieldsFunc(stmt, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' || r == ';'
		})[0])
		if mysqlClientCommands[first] || first == "?" {
			return "", errClientCommand
		}
	}
	return stmt, nil
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '$' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}

// isLineComment: "--" always starts a comment in Postgres; MySQL needs "-- " (a space or control
// character after it, or the end of input) and also takes "#".
func isLineComment(s string, i int, pg bool) bool {
	if strings.HasPrefix(s[i:], "--") {
		if pg {
			return true
		}
		return i+2 == len(s) || s[i+2] <= ' '
	}
	return !pg && s[i] == '#'
}

func skipLine(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j + 1
	}
	return len(s)
}

// skipBlockComment: Postgres nests /* */; MySQL does not, and its /*! … */ is executed, not skipped,
// so it is refused rather than guessed at.
func skipBlockComment(s string, i int, pg bool) (int, error) {
	if !pg && (strings.HasPrefix(s[i:], "/*!") || strings.HasPrefix(s[i:], "/*+")) {
		return 0, errors.New("mysql executable comments (/*! … */) are not allowed")
	}
	depth := 0
	for j := i; j < len(s)-1; j++ {
		switch {
		case s[j] == '/' && s[j+1] == '*':
			if depth == 0 || pg {
				depth++
			}
			j++
		case s[j] == '*' && s[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1, nil
			}
		}
	}
	return 0, errors.New("unterminated comment")
}

// skipQuoted returns the index just past the closing quote. A doubled quote is an escaped quote;
// with backslashEscapes a backslash escapes the next byte.
func skipQuoted(s string, i int, q byte, backslashEscapes bool) (int, bool) {
	for j := i + 1; j < len(s); j++ {
		switch {
		case backslashEscapes && s[j] == '\\':
			j++
		case s[j] == q:
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1, true
		}
	}
	return 0, false
}

// dollarTag reads a Postgres dollar-quote opener ($$ or $tag$) at i.
func dollarTag(s string, i int) (string, bool) {
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[i : j+1], true
		}
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (j > i+1 && c >= '0' && c <= '9') || c >= 0x80) {
			return "", false
		}
	}
	return "", false
}
