package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/cuihairu/ferry/server/internal/secret"
)

// TestSnapshotConfigFiles covers F-1926: operator-listed config files land in
// the backup dir each round, encrypted when a master key is configured,
// plaintext 0600 (with a warning) without one; a missing listed file is
// skipped, an empty list is a no-op, and an unwritable target fails the round.
func TestSnapshotConfigFiles(t *testing.T) {
	dir := t.TempDir()
	logger := quietLogger()
	src := filepath.Join(dir, "ferry.env")
	if err := os.WriteFile(src, []byte("FERRY_PORT=8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Empty list: nothing happens (opt-in, zero change).
	if err := snapshotConfigFiles(dir, nil, nil, logger); err != nil {
		t.Fatalf("empty list: %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "ferry-config-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty list must write nothing, got %v", entries)
	}

	// Plaintext path (no master key): exact copy at 0600; blank items are
	// skipped (comma splitting happens at config load, csvEnv).
	if err := snapshotConfigFiles(dir, []string{"", "   ", " " + src + " "}, nil, logger); err != nil {
		t.Fatalf("plaintext copy: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "ferry-config-*-ferry.env"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("plaintext copy missing: %v (err=%v)", matches, err)
	}
	plain := matches[0]
	data, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "FERRY_PORT=8080\n" {
		t.Fatalf("copy content mismatch: %q", data)
	}
	if fi, err := os.Stat(plain); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("copy perms = %v, want 0600 (err=%v)", fi.Mode().Perm(), err)
	}

	// Missing listed file: skipped with a warning, the round still succeeds.
	if err := snapshotConfigFiles(dir, []string{src, filepath.Join(dir, "absent.env")}, nil, logger); err != nil {
		t.Fatalf("missing file must be skipped: %v", err)
	}

	// Encrypted path: stream-encrypted under the master key, DecryptBytes round-trips.
	store := secret.NewStore("master-cfg")
	if !store.Enabled() {
		t.Fatal("store must be enabled with a master key")
	}
	if err := snapshotConfigFiles(dir, []string{src}, store, logger); err != nil {
		t.Fatalf("encrypted copy: %v", err)
	}
	encMatches, err := filepath.Glob(filepath.Join(dir, "ferry-config-*-ferry.env.enc"))
	if err != nil || len(encMatches) != 1 {
		t.Fatalf("encrypted copy missing: %v (err=%v)", encMatches, err)
	}
	encData, err := os.ReadFile(encMatches[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encData, []byte("FERRY_PORT")) {
		t.Fatal("encrypted copy leaks plaintext")
	}
	dec, err := store.DecryptBytes(encData)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(dec) != "FERRY_PORT=8080\n" {
		t.Fatalf("round-trip mismatch: %q", dec)
	}

	// Unwritable destination fails the round (dir replaced by a file).
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := snapshotConfigFiles(blocker, []string{src}, nil, logger); err == nil {
		t.Fatal("unwritable backup dir must fail the round")
	}
}
