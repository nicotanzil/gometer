package identity

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestDurableLoginEnvelopeBindsBrowserAndProtectsPKCE(t *testing.T) {
	f := fixture(t)
	provider, err := New(context.Background(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	login, _ := provider.Begin()
	code := f.code(t, login, nil, false)
	key := bytes.Repeat([]byte{17}, 32)
	sealed, err := SealLogin(login, key, "workspace/browser-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{login.verifier, login.nonce, login.state} {
		if bytes.Contains(sealed, []byte(secret)) {
			t.Fatal("pending login contains plaintext credential")
		}
	}
	for _, binding := range []string{"workspace/browser-b", "other-workspace/browser-a"} {
		if _, err := OpenLogin(sealed, key, binding); !errors.Is(err, ErrLogin) {
			t.Fatal("flow accepted a different browser/workspace binding")
		}
	}
	bad := append([]byte(nil), sealed...)
	bad[len(bad)-1] ^= 1
	if _, err := OpenLogin(bad, key, "workspace/browser-a"); !errors.Is(err, ErrLogin) {
		t.Fatal("tampered login accepted")
	}
	if _, err := OpenLogin(sealed, bytes.Repeat([]byte{18}, 32), "workspace/browser-a"); !errors.Is(err, ErrLogin) {
		t.Fatal("wrong key accepted")
	}
	restored, err := OpenLogin(sealed, key, "workspace/browser-a")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := provider.Finish(context.Background(), restored, login.State(), code)
	if err != nil || principal.Subject != "federated-subject-123" {
		t.Fatalf("restored flow failed: %v", err)
	}
	login.expiresAt = time.Now().Add(-time.Minute)
	if _, err := SealLogin(login, key, "workspace/browser-a"); !errors.Is(err, ErrExpired) {
		t.Fatal("expired flow sealed")
	}
}
