package geodns

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/dns"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestRegionRepsEmptyRegionAndTieBreak an empty region falls to "unknown"; with no probe data,
// the same group takes its representative by id ascending (stable-order fallback).
func TestRegionRepsEmptyRegionAndTieBreak(t *testing.T) {
	s, _ := newSyncer(t, &fakeChan{})
	first := addNode(t, s, "unknown-1", "", 0) // no RTT data
	addNode(t, s, "unknown-2", "", 0)
	reps, err := s.RegionReps(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reps["未知"]
	if !ok || got.ID != first.ID {
		t.Fatalf("reps = %#v; want unknown-region rep = first node", reps)
	}
}

// TestSyncNoFrontsNoop with an enabled DNS vendor but no front domain configured: reconciliation is a no-op (it has not
// reached the credential stage yet, and an unconfigured master key does not error either).
func TestSyncNoFrontsNoop(t *testing.T) {
	s, _ := newSyncer(t, &fakeChan{})
	if err := s.DB.Where("1=1").Delete(&storage.DNSFront{}).Error; err != nil {
		t.Fatal(err)
	}
	n, err := s.Sync(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("sync = %d, %v; want 0 nil", n, err)
	}
}

// TestSyncDecryptError a vendor's ciphertext cannot be decrypted (old ciphertext after a key rotation / tampered): reconciliation
// errors out and names the vendor.
func TestSyncDecryptError(t *testing.T) {
	s, _ := newSyncer(t, &fakeChan{})
	if err := s.DB.Model(&storage.DNSProvider{}).Where("1=1").
		Update("api_key", "v1:not-valid:cipher").Error; err != nil {
		t.Fatal(err)
	}
	_, err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "dns provider") {
		t.Fatalf("err = %v; want provider decrypt error", err)
	}
}

// TestSyncFactoryError channel-factory failure (unknown DNS vendor type etc.): reconciliation errors out.
func TestSyncFactoryError(t *testing.T) {
	s, _ := newSyncer(t, &fakeChan{})
	s.New = func(string, string) (dns.Provider, error) {
		return nil, errors.New("no such dns kind")
	}
	_, err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no such dns kind") {
		t.Fatalf("err = %v; want factory error", err)
	}
}
