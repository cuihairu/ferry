package roles

import "testing"

func TestRegistry(t *testing.T) {
	for _, name := range []string{"probe", "relay", "tunnel", "speedtest"} {
		if !Known(name) {
			t.Fatalf("component %q not registered", name)
		}
	}
	if Known("xray") {
		t.Fatal("xray is a managed proc, not a role component")
	}
	if !Standalone("probe") || !Standalone("relay") {
		t.Fatal("probe/relay must run standalone")
	}
	if Standalone("tunnel") || Standalone("speedtest") {
		t.Fatal("tunnel/speedtest are library roles")
	}
	if Standalone("nope") {
		t.Fatal("unknown component must not be standalone")
	}
}
