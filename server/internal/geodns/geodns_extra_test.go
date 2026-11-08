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

// TestRegionRepsEmptyRegionAndTieBreak 空区域归「未知」；无探测数据时同组
// 按 id 升序取代表（稳定序兜底）。
func TestRegionRepsEmptyRegionAndTieBreak(t *testing.T) {
	s, _ := newSyncer(t, &fakeChan{})
	first := addNode(t, s, "unknown-1", "", 0) // 无 RTT 数据
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

// TestSyncNoFrontsNoop 有启用 DNS 商但未配前置域名：对账 no-op（还没走到
// 凭证环节，未配主密钥也不报错）。
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

// TestSyncDecryptError DNS 商密文解不开（换钥后旧密文/被篡改）：对账报错
// 且指明是哪家。
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

// TestSyncFactoryError 通道工厂失败（未知 DNS 商类型等）：对账报错。
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
