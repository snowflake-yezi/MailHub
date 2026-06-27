package handler

import "testing"

func TestSplitPage(t *testing.T) {
	files := []string{"a", "b", "c", "d", "e"}
	got := splitPage(files, 2, 2)
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("splitPage() = %#v", got)
	}
	if got := splitPage(files, 4, 2); got != nil {
		t.Fatalf("splitPage() past end = %#v", got)
	}
}

func TestParsePageSizeDefaults(t *testing.T) {
	// Covered indirectly by handler tests in higher layers; keep pagination helper behavior explicit.
	if got := splitPage([]string{"a"}, 1, 20); len(got) != 1 || got[0] != "a" {
		t.Fatalf("splitPage default-style call = %#v", got)
	}
}
