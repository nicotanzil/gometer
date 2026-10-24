package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"time"
)

// SealLogin protects the short-lived PKCE verifier while a login waits in the
// database. Binding must identify both the workspace and initiating browser.
func SealLogin(login *Login, key []byte, binding string) ([]byte, error) {
	if login == nil || len(key) != 32 || binding == "" {
		return nil, ErrLogin
	}
	login.mu.Lock()
	defer login.mu.Unlock()
	if login.used || !time.Now().Before(login.expiresAt) {
		return nil, ErrExpired
	}
	data, err := json.Marshal(pendingLogin{login.state, login.nonce, login.verifier, login.expiresAt})
	if err != nil {
		return nil, ErrLogin
	}
	aead, err := loginCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, ErrLogin
	}
	return aead.Seal(nonce, nonce, data, []byte(binding)), nil
}

type pendingLogin struct {
	State     string    `json:"state"`
	Nonce     string    `json:"nonce"`
	Verifier  string    `json:"verifier"`
	ExpiresAt time.Time `json:"expires_at"`
}

// OpenLogin must follow atomic database consumption of the pending record.
// The cipher alone does not prevent replay of a previously valid record.
func OpenLogin(sealed, key []byte, binding string) (*Login, error) {
	if len(key) != 32 || binding == "" || len(sealed) > 16<<10 {
		return nil, ErrLogin
	}
	aead, err := loginCipher(key)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrLogin
	}
	data, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(binding))
	if err != nil {
		return nil, ErrLogin
	}
	var flow pendingLogin
	if json.Unmarshal(data, &flow) != nil || !validFlowValue(flow.State) || !validFlowValue(flow.Nonce) || !validFlowValue(flow.Verifier) {
		return nil, ErrLogin
	}
	if !time.Now().Before(flow.ExpiresAt) || flow.ExpiresAt.After(time.Now().Add(10*time.Minute)) {
		return nil, ErrExpired
	}
	return &Login{state: flow.State, nonce: flow.Nonce, verifier: flow.Verifier, expiresAt: flow.ExpiresAt}, nil
}
func validFlowValue(s string) bool {
	if len(s) != 43 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func loginCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrLogin
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrLogin
	}
	return aead, nil
}
