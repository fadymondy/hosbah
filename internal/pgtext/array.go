// Package pgtext holds the database/sql types sqlc needs for PostgreSQL columns that the pgx
// stdlib driver hands over as text. sqlc.yaml points such columns here (it would otherwise reach
// for lib/pq, which Hosbah does not depend on).
package pgtext

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
)

// Array is a one-dimensional text[] column. NULL elements read as "". A NULL array reads as empty.
type Array []string

// Value renders the array literal ({"a","b"}).
func (a Array) Value() (driver.Value, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}

// Scan parses the array literal.
func (a *Array) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case nil:
		*a = Array{}
		return nil
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("pgtext: cannot scan %T into Array", src)
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return errors.New("pgtext: not an array literal")
	}
	out := Array{}
	body := s[1 : len(s)-1]
	for i := 0; i < len(body); {
		var el strings.Builder
		if body[i] == '"' {
			i++
			for ; i < len(body) && body[i] != '"'; i++ {
				if body[i] == '\\' && i+1 < len(body) {
					i++
				}
				el.WriteByte(body[i])
			}
			if i >= len(body) {
				return errors.New("pgtext: unterminated quoted element")
			}
			i++ // closing quote
			out = append(out, el.String())
		} else {
			if body[i] == '{' {
				return errors.New("pgtext: nested arrays are not supported")
			}
			for ; i < len(body) && body[i] != ','; i++ {
				el.WriteByte(body[i])
			}
			if v := el.String(); v == "NULL" {
				out = append(out, "")
			} else {
				out = append(out, v)
			}
		}
		if i < len(body) {
			if body[i] != ',' {
				return errors.New("pgtext: malformed array literal")
			}
			i++
		}
	}
	*a = out
	return nil
}
