package state

import "testing"

func TestLayoutOf(t *testing.T) {
	st := NewState("123", "us-west-2")
	got := LayoutOf(st, "bucket", "table")
	if got.Target != "123/us-west-2" || got.Bucket != "bucket" || got.Note == "" {
		t.Fatalf("layout = %+v", got)
	}
	empty := LayoutOf(nil, "", "")
	if empty.Target != "/" {
		t.Fatalf("empty = %+v", empty)
	}
}
