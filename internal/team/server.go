// Package team serves the staged team sign-in boundary. Personal dashboard APIs
// are deliberately absent until shared secret and destination policies exist.
package team

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gometer/internal/identity"
	"gometer/internal/workspace"
)

const loginCookie = "__Host-gometer-login"
const sessionCookie = "__Host-gometer-session"
const CallbackPath = "/auth/callback"

type Options struct {
	Origin      string
	WorkspaceID string
	LoginKey    []byte
}
type Server struct {
	db                        *workspace.Database
	provider                  *identity.Provider
	origin, host, workspaceID string
	loginKey                  []byte
	exchanges                 chan struct{}
}

func New(db *workspace.Database, provider *identity.Provider, opts Options) (*Server, error) {
	u, err := url.Parse(opts.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(opts.Origin, "#") || !workspace.ValidID(opts.WorkspaceID) || len(opts.LoginKey) != 32 || db == nil || provider == nil || provider.RedirectURI() != opts.Origin+CallbackPath {
		return nil, fmt.Errorf("team sign-in requires an exact HTTPS origin, workspace ID and 32-byte login key")
	}
	return &Server{db: db, provider: provider, origin: opts.Origin, host: u.Host, workspaceID: opts.WorkspaceID, loginKey: append([]byte(nil), opts.LoginKey...), exchanges: make(chan struct{}, 16)}, nil
}

// Serve requires TLS termination at the configured origin. Forwarded headers do
// not select an origin, identity or callback URL.
func Serve(ctx context.Context, address string, s *Server, output io.Writer) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return fmt.Errorf("team listen address must contain an explicit IP and port")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("team listener unavailable")
	}
	defer listener.Close()
	server := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	stopped := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
			bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if server.Shutdown(bounded) != nil {
				server.Close()
			}
		case <-stopped:
		}
	}()
	fmt.Fprintf(output, "GoMeter team sign-in: %s\nTeam listener: %s\nTLS ingress is required. Team editing and execution are not enabled.\n", s.origin, listener.Addr())
	err = server.Serve(listener)
	close(stopped)
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self' "+s.provider.AuthorizationOrigin()+"; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	if r.Host != s.host {
		http.Error(w, "Use the configured team URL.", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.origin {
		http.Error(w, "Origin does not match the workspace.", http.StatusForbidden)
		return
	}
	// A Keycloak callback is a cross-site top-level navigation. It must still
	// prove possession of the browser cookie and the encrypted flow's state.
	callback := r.Method == "GET" && r.URL.Path == CallbackPath
	// Preserve top-level navigation from the company portal and the callback's
	// redirect chain, without allowing cross-site API or form requests.
	homeNavigation := r.Method == "GET" && r.URL.Path == "/" && r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" && !callback && !homeNavigation {
		http.Error(w, "Cross-site requests are not allowed.", http.StatusForbidden)
		return
	}
	if r.Method == "POST" && r.Header.Get("Origin") != s.origin {
		http.Error(w, "Origin is required.", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	switch {
	case r.URL.Path == "/favicon.ico" && r.Method == "GET":
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/" && r.Method == "GET":
		s.home(w, r)
	case r.URL.Path == "/assets/team.css" && r.Method == "GET":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		io.WriteString(w, stylesheet)
	case r.URL.Path == "/auth/login" && r.Method == "POST":
		s.begin(w, r)
	case callback:
		s.finish(w, r)
	case r.URL.Path == "/auth/logout" && r.Method == "POST":
		s.logout(w, r)
	case r.URL.Path == "/api/session" && r.Method == "GET":
		s.session(w, r)
	default:
		http.NotFound(w, r)
	}
}
func cookieValue(r *http.Request, name string) string {
	var value string
	seen := false
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			if seen {
				return ""
			}
			seen = true
			value = cookie.Value
		}
	}
	return value
}
func setCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: maxAge})
}
func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
func (s *Server) binding(browser string) string {
	return s.workspaceID + ":" + s.origin + ":" + browser
}
func (s *Server) begin(w http.ResponseWriter, r *http.Request) {
	login, err := s.provider.Begin()
	if err != nil {
		unavailable(w)
		return
	}
	browser, err := workspace.NewBrowserToken()
	if err != nil {
		unavailable(w)
		return
	}
	sealed, err := identity.SealLogin(login, s.loginKey, s.binding(browser))
	if err != nil {
		unavailable(w)
		return
	}
	if err = s.db.SaveLogin(r.Context(), s.workspaceID, browser, cookieValue(r, loginCookie), sealed, login.ExpiresAt()); err != nil {
		unavailable(w)
		return
	}
	setCookie(w, loginCookie, browser, login.ExpiresAt())
	http.Redirect(w, r, login.AuthorizationURL(), http.StatusSeeOther)
}
func (s *Server) finish(w http.ResponseWriter, r *http.Request) {
	// Bound outbound token/key requests even before ingress rate controls.
	select {
	case s.exchanges <- struct{}{}:
		defer func() { <-s.exchanges }()
	default:
		unavailable(w)
		return
	}
	browser := cookieValue(r, loginCookie)
	clearCookie(w, loginCookie)
	sealed, err := s.db.ConsumeLogin(r.Context(), s.workspaceID, browser)
	if errors.Is(err, workspace.ErrUnavailable) {
		unavailable(w)
		return
	}
	if err != nil {
		loginFailed(w)
		return
	}
	login, err := identity.OpenLogin(sealed, s.loginKey, s.binding(browser))
	if err != nil || len(r.URL.RawQuery) > 8192 {
		loginFailed(w)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["state"]) != 1 || len(query["code"]) != 1 || len(query["error"]) != 0 {
		loginFailed(w)
		return
	}
	principal, err := s.provider.Finish(r.Context(), login, query.Get("state"), query.Get("code"))
	if err != nil {
		loginFailed(w)
		return
	}
	token, session, err := s.db.NewSession(r.Context(), s.workspaceID, principal.Issuer, principal.Subject, principal.ExpiresAt, cookieValue(r, sessionCookie))
	if errors.Is(err, workspace.ErrUnavailable) {
		unavailable(w)
		return
	}
	if err != nil {
		http.Error(w, "Your account does not have enabled membership in this workspace.", http.StatusForbidden)
		return
	}
	setCookie(w, sessionCookie, token, session.ExpiresAt)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func csrfToken(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("gometer/team/csrf/v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	token := cookieValue(r, sessionCookie)
	session, err := s.db.ResolveSession(r.Context(), s.workspaceID, token)
	if err != nil {
		authError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		workspace.Session
		CSRF string `json:"csrf_token"`
	}{session, csrfToken(token)})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	token := cookieValue(r, sessionCookie)
	if _, err := s.db.ResolveSession(r.Context(), s.workspaceID, token); err != nil {
		authError(w, err)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		http.Error(w, "Use the sign-out form.", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil || len(r.PostForm["csrf_token"]) != 1 || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf_token")), []byte(csrfToken(token))) != 1 {
		http.Error(w, "Sign-out request could not be verified.", http.StatusForbidden)
		return
	}
	if err := s.db.RevokeSession(r.Context(), s.workspaceID, token); err != nil {
		authError(w, err)
		return
	}
	clearCookie(w, sessionCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func unavailable(w http.ResponseWriter) {
	http.Error(w, "Workspace sign-in is temporarily unavailable. Try again later.", http.StatusServiceUnavailable)
}
func loginFailed(w http.ResponseWriter) {
	http.Error(w, "Sign-in could not be verified. Return to the workspace and start again.", http.StatusBadRequest)
}
func authError(w http.ResponseWriter, err error) {
	if errors.Is(err, workspace.ErrUnavailable) {
		unavailable(w)
		return
	}
	http.Error(w, "Sign in to access this workspace.", http.StatusUnauthorized)
}
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	// A no-referrer document makes Chromium serialize a form POST's Origin as
	// null. Same-origin preserves strict form checks without cross-site referrers.
	// Callback responses retain no-referrer to protect authorization codes.
	w.Header().Set("Referrer-Policy", "same-origin")
	data := struct {
		Member *workspace.Member
		CSRF   string
	}{}
	token := cookieValue(r, sessionCookie)
	if token != "" {
		session, err := s.db.ResolveSession(r.Context(), s.workspaceID, token)
		if errors.Is(err, workspace.ErrUnavailable) {
			unavailable(w)
			return
		}
		if err == nil {
			data.Member = &session.Member
			data.CSRF = csrfToken(token)
		} else {
			clearCookie(w, sessionCookie)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	homeTemplate.Execute(w, data)
}

var homeTemplate = template.Must(template.New("home").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>GoMeter · Team sign-in</title><link rel="stylesheet" href="/assets/team.css"></head><body><main><div class="brand">GoMeter <span>TEAM WORKSPACE</span></div>{{if .Member}}<p class="eyebrow">CONNECTED</p><h1>Welcome, {{.Member.Name}}</h1><p>You are signed in as <strong>{{.Member.Role}}</strong>.</p><p class="notice">Team test editing and execution are not available on this service yet.</p><form action="/auth/logout" method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><button type="submit">Sign out of GoMeter</button></form><p class="footnote">Signing out here ends your GoMeter session. Your company SSO session stays active.</p>{{else}}<p class="eyebrow">COMPANY ACCOUNT</p><h1>Sign in to your workspace</h1><p>Continue with your company account through Keycloak.</p><form action="/auth/login" method="post"><button type="submit">Sign in with company account <span aria-hidden="true">→</span></button></form><p class="footnote">Your directory password is handled by your company identity provider.</p>{{end}}</main></body></html>`))

const stylesheet = `:root{color-scheme:light dark;font:16px/1.6 system-ui,sans-serif;color:#172334;background:#f3f5f8}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px}main{width:min(100%,540px);padding:40px;background:#fff;border:1px solid #dce2ea;border-radius:12px;box-shadow:0 12px 36px #162a4510}.brand{font-size:22px;font-weight:750;display:flex;gap:14px;align-items:center;margin-bottom:44px}.brand span,.eyebrow{font-size:11px;letter-spacing:.12em;font-weight:650;color:#5a6c82}.brand span{border-left:1px solid #dce2ea;padding-left:14px}h1{font-size:28px;line-height:1.25;letter-spacing:-.035em;margin:10px 0 16px;overflow-wrap:anywhere}p{color:#536176}button{font:inherit;background:#245cd8;color:#fff;border:0;border-radius:7px;padding:12px 18px;font-weight:600;cursor:pointer;text-align:left;width:100%;margin-top:16px;display:flex;justify-content:space-between;gap:16px}button:hover{background:#194bb5}button:focus-visible{outline:3px solid #88b1ff;outline-offset:4px}.footnote{font-size:13px;margin:18px 0 0}.notice{padding:12px 14px;background:#f3f5f8;border-radius:6px;font-size:14px}@media(max-width:480px){main{padding:26px}.brand{display:block}.brand span{display:block;border:0;padding:5px 0 0}}@media(prefers-color-scheme:dark){:root{background:#111822;color:#e9edf5}main{background:#1a2432;border-color:#344258}p{color:#afbdd2}.brand span,.eyebrow{color:#9dacc3}.notice{background:#111c2a}}`
