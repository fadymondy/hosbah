package pgtext

import (
	"reflect"
	"testing"
)

func TestArrayRoundTrip(t *testing.T) {
	for _, in := range []Array{{}, {"a@example.test"}, {"a b", `q"uo\te`, "c,d", "{x}", ""}} {
		v, err := in.Value()
		if err != nil {
			t.Fatal(err)
		}
		var out Array
		if err := out.Scan(v); err != nil {
			t.Fatalf("%v: %v", v, err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Errorf("%v -> %v -> %v", in, v, out)
		}
	}
	var a Array
	if err := a.Scan(`{plain,"two words",NULL}`); err != nil || !reflect.DeepEqual(a, Array{"plain", "two words", ""}) {
		t.Errorf("server form: %v %v", a, err)
	}
	if err := a.Scan([]byte(`{}`)); err != nil || len(a) != 0 {
		t.Errorf("empty: %v %v", a, err)
	}
	if a.Scan(`nope`) == nil || a.Scan(`{"open}`) == nil || a.Scan(`{{a}}`) == nil {
		t.Error("malformed literals must fail")
	}
}
