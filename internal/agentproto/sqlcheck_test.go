package agentproto

import "testing"

func TestSingleSQLStatement(t *testing.T) {
	cases := []struct {
		kind, sql, want string
		ok              bool
	}{
		{"postgres", "SELECT 1", "SELECT 1", true},
		{"postgres", "  SELECT 1 ;  \n", "SELECT 1", true},
		{"postgres", "SELECT 1; -- done\n/* bye */", "SELECT 1", true},
		{"postgres", "SELECT ';' AS x", "SELECT ';' AS x", true},
		{"postgres", "SELECT $$a;b$$, $f$ ; $f$", "SELECT $$a;b$$, $f$ ; $f$", true},
		{"postgres", "SELECT 1 -- ; DROP TABLE t", "SELECT 1 -- ; DROP TABLE t", true},
		{"postgres", "SELECT /* ; /* nested ; */ */ 1", "SELECT /* ; /* nested ; */ */ 1", true},
		{"postgres", `SELECT "a;b" FROM t`, `SELECT "a;b" FROM t`, true},
		{"postgres", "SELECT 'it''s; fine'", "SELECT 'it''s; fine'", true},

		{"postgres", "SELECT 1; COMMIT; DROP TABLE t", "", false},
		{"postgres", "SELECT 1;;DROP TABLE t", "", false},
		{"postgres", `\! ls`, "", false},
		{"postgres", "SELECT 1 \\g", "", false},
		{"postgres", `SELECT E'\\';' ; DROP TABLE t`, "", false},
		{"postgres", `SELECT E'\';DROP TABLE t;'`, `SELECT E'\';DROP TABLE t;'`, true},
		{"postgres", "SELECT 'open", "", false},
		{"postgres", "", "", false},
		{"postgres", " ; ", "", false},

		{"mysql", "SELECT 1", "SELECT 1", true},
		{"mysql", "SELECT 'a\\';b' ;", "SELECT 'a\\';b'", true},
		{"mysql", "SELECT `a;b` FROM t # c\n", "SELECT `a;b` FROM t # c", true},
		{"mysql", "SELECT 1; # trailing", "SELECT 1", true},
		{"mysql", "SELECT 1--1; DROP TABLE t", "", false},
		{"mysql", "system ls", "", false},
		{"mysql", "SOURCE /etc/passwd", "", false},
		{"mysql", "SELECT 1 /*! ; DROP TABLE t */", "", false},
		{"mysql", `\! ls`, "", false},
		{"mysql", "SELECT 1; DROP TABLE t", "", false},
	}
	for _, c := range cases {
		got, err := SingleSQLStatement(c.kind, c.sql)
		if c.ok != (err == nil) {
			t.Errorf("%s %q: err = %v, want ok=%v", c.kind, c.sql, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("%s %q: got %q, want %q", c.kind, c.sql, got, c.want)
		}
	}
}
