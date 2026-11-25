package ringlog

import "testing"

// TestDefaultAndNewEdge covers the Default singleton and New's capacity lower bound.
func TestDefaultAndNewEdge(t *testing.T) {
	if Default() == nil {
		t.Fatal("Default nil")
	}
	l := New(0)
	if l == nil || cap(l.ring) != 1 {
		t.Fatalf("New(0) should clamp to 1, got %d", cap(l.ring))
	}
	l2 := New(5)
	if len(l2.ring) != 5 {
		t.Fatalf("New(5) len=%d", len(l2.ring))
	}
	// Write one line to make sure the Add path is exercised.
	l.Add("x")
	if got := l.Entries(10); len(got) != 1 {
		t.Fatalf("entries=%d", len(got))
	}
}
