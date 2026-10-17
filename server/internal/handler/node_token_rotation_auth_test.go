package handler

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

func TestAuthNodeTokenRotationWindow(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	rotated := time.Now().Add(-time.Minute)
	node := storage.Node{Name: "grace", Address: "grace.example.com", Port: 443,
		Protocol: "vless", Token: "current", OldToken: "former", TokenRotatedAt: &rotated,
		Enabled: true, Status: "online"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := &Handler{db: db}
	for _, token := range []string{"current", "former"} {
		if got, err := h.authNode(token); err != nil || got != int64(node.ID) {
			t.Fatalf("auth %q = %d err=%v", token, got, err)
		}
	}

	expired := time.Now().Add(-nodeTokenGracePeriod - time.Second)
	if err := db.Model(&storage.Node{}).Where("id=?", node.ID).
		Update("token_rotated_at", expired).Error; err != nil {
		t.Fatalf("expire grace: %v", err)
	}
	if got, err := h.authNode("current"); err != nil || got != int64(node.ID) {
		t.Fatalf("current after expiry = %d err=%v", got, err)
	}
	if _, err := h.authNode("former"); err == nil || err.Error() != "invalid token" {
		t.Fatalf("former after expiry err=%v", err)
	}

	if err := db.Model(&storage.Node{}).Where("id=?", node.ID).
		Update("token_rotated_at", time.Now()).Error; err != nil {
		t.Fatalf("reopen grace: %v", err)
	}
	if err := db.Create(&storage.NodeTokenRevocation{NodeID: node.ID, Token: "former", RevokedAt: time.Now()}).Error; err != nil {
		t.Fatalf("seed revocation: %v", err)
	}
	var count int64
	if err := db.Model(&storage.NodeTokenRevocation{}).Where("token = ?", "former").Count(&count).Error; err != nil {
		t.Fatalf("count revocation: %v", err)
	}
	if count == 0 {
		t.Fatalf("revocation count = 0")
	}
	if _, err := h.authNode("former"); err == nil || err.Error() != "revoked token" {
		t.Fatalf("revoked err=%v", err)
	}
}
