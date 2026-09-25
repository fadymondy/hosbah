package agent

import (
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func strp(s string) *string { return &s }

func TestParsePSQLCSV(t *testing.T) {
	out := "id,name,note\n" +
		"1,alice," + dbQueryNullSentinel + "\n" +
		"2,\"bob, jr\",\"\"\n"
	cols, rows, err := parsePSQLCSV(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantCols := []string{"id", "name", "note"}
	if len(cols) != len(wantCols) {
		t.Fatalf("cols = %v", cols)
	}
	for i, c := range wantCols {
		if cols[i] != c {
			t.Errorf("col %d = %q, want %q", i, cols[i], c)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[0][2] != nil {
		t.Errorf("row 0 note should be NULL, got %v", rows[0][2])
	}
	if rows[1][1] == nil || *rows[1][1] != "bob, jr" {
		t.Errorf("row 1 name = %v, want %q", rows[1][1], "bob, jr")
	}
	if rows[1][2] == nil || *rows[1][2] != "" {
		t.Errorf("row 1 note should be an empty string, not NULL: %v", rows[1][2])
	}
}

func TestParsePSQLCSVEmpty(t *testing.T) {
	cols, rows, err := parsePSQLCSV("")
	if err != nil || cols != nil || rows != nil {
		t.Fatalf("empty output should parse to nothing: cols=%v rows=%v err=%v", cols, rows, err)
	}
}

func TestParsePSQLCSVHeaderOnly(t *testing.T) {
	cols, rows, err := parsePSQLCSV("id,name\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 2 || len(rows) != 0 {
		t.Fatalf("cols=%v rows=%v", cols, rows)
	}
}

func TestParseMySQLBatch(t *testing.T) {
	out := "id\tname\tnote\n" +
		"1\talice\t\\N\n" +
		"2\tbob\\tjr\tline1\\nline2\n"
	cols, rows, err := parseMySQLBatch(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 3 || cols[1] != "name" {
		t.Fatalf("cols = %v", cols)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[0][2] != nil {
		t.Errorf("row 0 note should be NULL, got %v", rows[0][2])
	}
	if rows[1][1] == nil || *rows[1][1] != "bob\tjr" {
		t.Errorf("row 1 name = %v, want %q (unescaped tab)", rows[1][1], "bob\tjr")
	}
	if rows[1][2] == nil || *rows[1][2] != "line1\nline2" {
		t.Errorf("row 1 note = %v, want %q (unescaped newline)", rows[1][2], "line1\nline2")
	}
}

func TestParseMySQLBatchEmpty(t *testing.T) {
	cols, rows, err := parseMySQLBatch("")
	if err != nil || cols != nil || rows != nil {
		t.Fatalf("empty output should parse to nothing: cols=%v rows=%v err=%v", cols, rows, err)
	}
}

func TestUnescapeMySQLBatch(t *testing.T) {
	cases := map[string]string{
		`plain`:        "plain",
		`a\tb`:         "a\tb",
		`a\nb`:         "a\nb",
		`a\\b`:         `a\b`,
		"a\\0b":        "a\x00b",
		`no\Xtrailing`: `no\Xtrailing`, // an unknown escape is left as-is
	}
	for in, want := range cases {
		if got := unescapeMySQLBatch(in); got != want {
			t.Errorf("unescapeMySQLBatch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCapResultSmall(t *testing.T) {
	res := &agentproto.DBQueryResult{
		Columns: []agentproto.DBQueryColumn{{Name: "id"}, {Name: "name"}},
		Rows:    [][]*string{{strp("1"), strp("alice")}, {strp("2"), nil}},
	}
	capResult(res)
	if res.RowCount != 2 || res.Truncated {
		t.Fatalf("small result should not be truncated: %+v", res)
	}
	if res.Rows[1][1] != nil {
		t.Errorf("a nil cell should stay nil, got %v", res.Rows[1][1])
	}
}

func TestCapResultCellTruncation(t *testing.T) {
	big := make([]byte, agentproto.DBQueryMaxCellBytes+100)
	for i := range big {
		big[i] = 'x'
	}
	v := string(big)
	res := &agentproto.DBQueryResult{
		Columns: []agentproto.DBQueryColumn{{Name: "note"}},
		Rows:    [][]*string{{&v}},
	}
	capResult(res)
	if !res.Truncated {
		t.Fatalf("an over-size cell should mark the result truncated")
	}
	if len(*res.Rows[0][0]) != agentproto.DBQueryMaxCellBytes {
		t.Errorf("cell was not clipped to the cap: got %d bytes", len(*res.Rows[0][0]))
	}
}

func TestCapResultSizeTruncation(t *testing.T) {
	row := make([]byte, 4000)
	for i := range row {
		row[i] = 'y'
	}
	v := string(row)
	var rows [][]*string
	// Enough rows of ~4KB each to exceed the 1 MiB total cap.
	for i := 0; i < 400; i++ {
		rows = append(rows, []*string{&v})
	}
	res := &agentproto.DBQueryResult{Columns: []agentproto.DBQueryColumn{{Name: "note"}}, Rows: rows}
	capResult(res)
	if !res.Truncated {
		t.Fatalf("a result over the total size cap should be marked truncated")
	}
	if res.RowCount >= 400 {
		t.Errorf("rows should have been dropped once the size cap was hit, got %d", res.RowCount)
	}
}
