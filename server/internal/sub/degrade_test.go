package sub

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// unsupportedField is one node carrying a field the target schema cannot
// express: the transport is outside the protocol's net enum.
func unsupportedField() storage.Node {
	return storage.Node{Name: "kcp", Address: "k.example.com", Port: 443,
		Protocol: "vless", Config: `{"uuid":"u","net":"kcp"}`}
}

// unknownExtraField is one node whose config carries an extension key no
// adapter knows: the render must degrade (drop it), never leak it into the
// client output.
func unknownExtraField() storage.Node {
	return storage.Node{Name: "ext", Address: "e.example.com", Port: 443,
		Protocol: "vless", Config: `{"uuid":"u","vendor":"acme"}`}
}

// TestUnsupportedFieldClearError pins the error surface (F-1130): a field the
// target schema cannot express is a clear pack-time error naming the value,
// on every adapter family -- never a silently mangled client config (the
// same convention as the missing-required-field errors).
func TestUnsupportedFieldClearError(t *testing.T) {
	node := unsupportedField()
	entries := []Entry{{Node: node}}
	if _, err := ShareLink(&node); err == nil || !strings.Contains(err.Error(), "kcp") {
		t.Fatalf("share link err = %v", err)
	}
	if _, err := PackV2Ray(entries, nil); err == nil || !strings.Contains(err.Error(), "kcp") {
		t.Fatalf("v2ray pack err = %v", err)
	}
	if _, err := PackClash(entries, nil); err == nil || !strings.Contains(err.Error(), "kcp") {
		t.Fatalf("clash pack err = %v", err)
	}
	if _, err := PackMihomo(entries, nil); err == nil || !strings.Contains(err.Error(), "kcp") {
		t.Fatalf("mihomo pack err = %v", err)
	}
	if _, err := PackSingBox(entries); err == nil || !strings.Contains(err.Error(), "kcp") {
		t.Fatalf("singbox pack err = %v", err)
	}
	if _, err := PackXray(entries); err == nil || !strings.Contains(err.Error(), "kcp") {
		t.Fatalf("xray pack err = %v", err)
	}
}

// TestUnknownExtraFieldDegrades pins the degrade surface (F-1130, with
// F-1131): an unknown config extension key is dropped by every adapter --
// the pack still succeeds and the key never reaches the client output.
func TestUnknownExtraFieldDegrades(t *testing.T) {
	node := unknownExtraField()
	entries := []Entry{{Node: node}}
	out, err := PackV2Ray(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "acme") {
		t.Fatalf("v2ray link leaked the extra key: %q", string(raw))
	}
	clashOut, err := PackClash(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clashOut, "acme") {
		t.Fatalf("clash proxy leaked the extra key:\n%s", clashOut)
	}
	sbOut, err := PackSingBox(entries)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sbOut, "acme") {
		t.Fatalf("singbox outbound leaked the extra key:\n%s", sbOut)
	}
	xrOut, err := PackXray(entries)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(xrOut, "acme") {
		t.Fatalf("xray outbound leaked the extra key:\n%s", xrOut)
	}
}
