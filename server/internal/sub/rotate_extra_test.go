package sub

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

func mkEntry(name string) Entry { return Entry{Node: storage.Node{Name: name}} }

// TestRotateRunNegativeOffset covers the negative-offset normalization branch of rotateRun.
func TestRotateRunNegativeOffset(t *testing.T) {
	seg := []Entry{mkEntry("a"), mkEntry("b"), mkEntry("c"), mkEntry("d")}
	rotateRun(seg, 0, 4, -1)
	// Negative offset normalizes to +3: a->d, b->a, c->b, d->c.
	if seg[0].Node.Name != "d" || seg[1].Node.Name != "a" || seg[2].Node.Name != "b" || seg[3].Node.Name != "c" {
		t.Fatalf("seg = %+v", seg)
	}
	// A single-element segment returns immediately.
	rotateRun(seg, 0, 1, 5)
	// offset 0 returns.
	rotateRun(seg, 0, 4, 0)
	if seg[0].Node.Name != "d" {
		t.Fatalf("zero offset should noop: %+v", seg)
	}
}
