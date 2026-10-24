// Package identity verifies OIDC identity. LDAP authentication and federation
// belong to Keycloak; GoMeter never receives an LDAP bind password.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var (
	ErrConfiguration = errors.New("OIDC configuration or discovery is invalid; check issuer, client, redirect URI and certificate trust")
	ErrLogin         = errors.New("OIDC sign-in could not be verified; start a new sign-in")
	ErrExpired       = errors.New("OIDC sign-in attempt expired; start a new sign-in")
)

type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	CAFile       string
}

type Principal struct {
	Issuer      string    `json:"issuer"`
	Subject     string    `json:"subject"`
	DisplayName string    `json:"display_name"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Provider struct {
	issuer   string
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}

// Login is a single-use browser flow, not an authenticated application session.
// The HTTP adapter must bind its state to the initiating browser and atomically
// consume a durable record before restoring it with OpenLogin.
type Login struct {
	mu                                       sync.Mutex
	state, nonce, verifier, authorizationURL string
	expiresAt                                time.Time
	used                                     bool
}

func (l *Login) AuthorizationURL() string { return l.authorizationURL }
func (l *Login) State() string            { return l.state }
func (l *Login) ExpiresAt() time.Time     { return l.expiresAt }

func validText(value string, limit int) bool {
	return value != "" && len(value) <= limit && value == strings.TrimSpace(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
func secureURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && !strings.Contains(raw, "#") && u.Opaque == ""
}
func (c Config) validate() bool {
	if !secureURL(c.Issuer) || !secureURL(c.RedirectURI) || !validText(c.ClientID, 256) || len(c.ClientSecret) == 0 || len(c.ClientSecret) > 4096 {
		return false
	}
	issuer, _ := url.Parse(c.Issuer)
	redirect, _ := url.Parse(c.RedirectURI)
	return issuer.RawQuery == "" && !issuer.ForceQuery && redirect.RawQuery == "" && !redirect.ForceQuery
}

type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type limitedBody struct {
	io.Reader
	io.Closer
}

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		return nil, ErrConfiguration
	}
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > 1<<20 {
		response.Body.Close()
		return nil, ErrConfiguration
	}
	response.Body = limitedBody{Reader: io.LimitReader(response.Body, (1<<20)+1), Closer: response.Body}
	return response, nil
}

func New(ctx context.Context, c Config) (*Provider, error) {
	if !c.validate() {
		return nil, ErrConfiguration
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if c.CAFile != "" {
		data, err := os.ReadFile(c.CAFile)
		if err != nil || !roots.AppendCertsFromPEM(data) {
			return nil, ErrConfiguration
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, MaxIdleConns: 8, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10}
	client := &http.Client{Transport: boundedTransport{transport}, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	provider, err := newProvider(ctx, c, client)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return provider, nil
}
func newProvider(ctx context.Context, c Config, client *http.Client) (*Provider, error) {
	if !c.validate() {
		return nil, ErrConfiguration
	}
	// A long-lived context supplies transport to the JWKS key set; request
	// cancellation must not permanently cancel later key refreshes.
	keyContext := oidc.ClientContext(context.WithoutCancel(ctx), client)
	discovery, err := oidc.NewProvider(oidc.ClientContext(ctx, client), c.Issuer)
	if err != nil {
		return nil, ErrConfiguration
	}
	var metadata struct {
		JWKS string `json:"jwks_uri"`
	}
	if discovery.Claims(&metadata) != nil || !secureURL(metadata.JWKS) {
		return nil, ErrConfiguration
	}
	endpoints := discovery.Endpoint()
	if !secureURL(endpoints.AuthURL) || !secureURL(endpoints.TokenURL) {
		return nil, ErrConfiguration
	}
	endpoints.AuthStyle = oauth2.AuthStyleInHeader
	verifier := oidc.NewVerifier(c.Issuer, oidc.NewRemoteKeySet(keyContext, metadata.JWKS), &oidc.Config{ClientID: c.ClientID, SupportedSigningAlgs: []string{oidc.RS256}})
	return &Provider{issuer: c.Issuer, client: client, verifier: verifier, config: oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: c.RedirectURI, Endpoint: endpoints, Scopes: []string{oidc.ScopeOpenID, "profile"}}}, nil
}
func (p *Provider) Close() { p.client.CloseIdleConnections() }
func randomValue() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrLogin
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func (p *Provider) Begin() (*Login, error) {
	state, err := randomValue()
	if err != nil {
		return nil, err
	}
	nonce, err := randomValue()
	if err != nil {
		return nil, err
	}
	verifier, err := randomValue()
	if err != nil {
		return nil, err
	}
	login := &Login{state: state, nonce: nonce, verifier: verifier, expiresAt: time.Now().Add(10 * time.Minute)}
	login.authorizationURL = p.config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	return login, nil
}
func (p *Provider) Finish(ctx context.Context, login *Login, state, code string) (Principal, error) {
	if login == nil {
		return Principal{}, ErrLogin
	}
	login.mu.Lock()
	if login.used || subtle.ConstantTimeCompare([]byte(state), []byte(login.state)) != 1 || code == "" || len(code) > 4096 {
		login.mu.Unlock()
		return Principal{}, ErrLogin
	}
	if time.Now().After(login.expiresAt) {
		login.used = true
		login.mu.Unlock()
		return Principal{}, ErrExpired
	}
	login.used = true
	login.mu.Unlock()
	token, err := p.config.Exchange(oidc.ClientContext(ctx, p.client), code, oauth2.VerifierOption(login.verifier))
	if err != nil {
		return Principal{}, ErrLogin
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) > 64<<10 {
		return Principal{}, ErrLogin
	}
	verified, err := p.verifier.Verify(ctx, raw)
	if err != nil || verified.Issuer != p.issuer || !validText(verified.Subject, 512) || subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(login.nonce)) != 1 {
		return Principal{}, ErrLogin
	}
	if verified.AccessTokenHash != "" && verified.VerifyAccessToken(token.AccessToken) != nil {
		return Principal{}, ErrLogin
	}
	var claims struct {
		Name            string `json:"name"`
		Username        string `json:"preferred_username"`
		AuthorizedParty string `json:"azp"`
	}
	if verified.Claims(&claims) != nil || (claims.AuthorizedParty != "" && claims.AuthorizedParty != p.config.ClientID) || (len(verified.Audience) > 1 && claims.AuthorizedParty == "") {
		return Principal{}, ErrLogin
	}
	name := claims.Name
	if !validText(name, 256) {
		name = claims.Username
	}
	if !validText(name, 256) {
		name = verified.Subject
	}
	return Principal{Issuer: p.issuer, Subject: verified.Subject, DisplayName: name, ExpiresAt: verified.Expiry}, nil
}

// RedirectURI is the exact callback registered for this provider.
func (p *Provider) RedirectURI() string { return p.config.RedirectURL }

// AuthorizationOrigin allows the sign-in form to redirect only to this provider.
func (p *Provider) AuthorizationOrigin() string {
	u, _ := url.Parse(p.config.Endpoint.AuthURL)
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}
