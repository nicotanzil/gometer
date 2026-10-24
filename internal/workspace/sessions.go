package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"
)

// NewBrowserToken creates a 256-bit opaque cookie value. Only its digest belongs
// in database indexes; the raw value must not appear in logs or audit records.
func NewBrowserToken() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", ErrUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}
func tokenHash(token string) (string, bool) {
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) != 32 || base64.RawURLEncoding.EncodeToString(data) != token {
		return "", false
	}
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:]), true
}

// SaveLogin replaces this browser's previous pending flow. The encrypted flow
// must be bound to this workspace and browser before calling this method.
func (d *Database) SaveLogin(ctx context.Context, workspaceID, browser, previous string, sealed []byte, expires time.Time) error {
	hash, ok := tokenHash(browser)
	if !ok || !ValidID(workspaceID) || len(sealed) == 0 || len(sealed) > 16<<10 || !expires.After(time.Now()) || expires.After(time.Now().Add(10*time.Minute)) {
		return ErrForbidden
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var ignored string
	err = tx.QueryRow(ctx, `SELECT id FROM gometer.workspaces WHERE id=$1 FOR UPDATE`, workspaceID).Scan(&ignored)
	if err == pgx.ErrNoRows {
		return ErrForbidden
	}
	if err != nil {
		return ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `DELETE FROM gometer.pending_logins WHERE workspace_id=$1 AND expires_at<=clock_timestamp()`, workspaceID); err != nil {
		return ErrUnavailable
	}
	if old, ok := tokenHash(previous); ok {
		if _, err = tx.Exec(ctx, `DELETE FROM gometer.pending_logins WHERE workspace_id=$1 AND browser_hash=$2`, workspaceID, old); err != nil {
			return ErrUnavailable
		}
	}
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM gometer.pending_logins WHERE workspace_id=$1`, workspaceID).Scan(&count) != nil {
		return ErrUnavailable
	}
	if count >= 1000 {
		return ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.pending_logins(workspace_id,browser_hash,sealed_flow,expires_at) VALUES($1,$2,$3,$4)`, workspaceID, hash, sealed, expires); err != nil {
		return ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}

// ConsumeLogin deletes before returning, so only one replica may exchange a code.
func (d *Database) ConsumeLogin(ctx context.Context, workspaceID, browser string) ([]byte, error) {
	hash, ok := tokenHash(browser)
	if !ok {
		return nil, ErrForbidden
	}
	var data []byte
	var valid bool
	err := d.pool.QueryRow(ctx, `DELETE FROM gometer.pending_logins WHERE workspace_id=$1 AND browser_hash=$2 RETURNING sealed_flow,expires_at>clock_timestamp()`, workspaceID, hash).Scan(&data, &valid)
	if err == pgx.ErrNoRows {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if !valid {
		return nil, ErrForbidden
	}
	return data, nil
}

type Session struct {
	Member    Member    `json:"member"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewSession accepts only an already verified OIDC identity. It checks current
// enabled membership again, rotates the prior browser session and audits login.
func (d *Database) NewSession(ctx context.Context, workspaceID, issuer, subject string, expires time.Time, previous string) (string, Session, error) {
	if !expires.After(time.Now()) {
		return "", Session{}, ErrForbidden
	}
	if maximum := time.Now().Add(8 * time.Hour); expires.After(maximum) {
		expires = maximum
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", Session{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	id, err := sessionMembership(ctx, tx, workspaceID, issuer, subject)
	if err != nil {
		return "", Session{}, err
	}
	role, err := require(ctx, tx, workspaceID, id, false)
	if err != nil {
		return "", Session{}, err
	}
	token, err := NewBrowserToken()
	if err != nil {
		return "", Session{}, err
	}
	hash, _ := tokenHash(token)
	sessionID, err := NewID()
	if err != nil {
		return "", Session{}, ErrUnavailable
	}
	if old, ok := tokenHash(previous); ok {
		if _, err = tx.Exec(ctx, `DELETE FROM gometer.sessions WHERE workspace_id=$1 AND token_hash=$2`, workspaceID, old); err != nil {
			return "", Session{}, ErrUnavailable
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM gometer.sessions WHERE workspace_id=$1 AND expires_at<=clock_timestamp()`, workspaceID); err != nil {
		return "", Session{}, ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.sessions(id,workspace_id,actor_id,token_hash,expires_at) VALUES($1,$2,$3,$4,$5)`, sessionID, workspaceID, id, hash, expires); err != nil {
		return "", Session{}, ErrUnavailable
	}
	if audit(ctx, tx, workspaceID, id, "session.login", sessionID, 0) != nil {
		return "", Session{}, ErrUnavailable
	}
	var name string
	if tx.QueryRow(ctx, `SELECT display_name FROM gometer.actors WHERE id=$1`, id).Scan(&name) != nil {
		return "", Session{}, ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return "", Session{}, ErrUnavailable
	}
	return token, Session{Member: Member{ID: id, Name: name, Role: role, Enabled: true}, ExpiresAt: expires}, nil
}

// ResolveSession never trusts a cached role or the browser's account details.
func (d *Database) ResolveSession(ctx context.Context, workspaceID, token string) (Session, error) {
	hash, ok := tokenHash(token)
	if !ok {
		return Session{}, ErrForbidden
	}
	var session Session
	err := d.pool.QueryRow(ctx, `SELECT a.id,a.display_name,m.role,m.enabled,s.expires_at FROM gometer.sessions s JOIN gometer.memberships m ON m.workspace_id=s.workspace_id AND m.actor_id=s.actor_id JOIN gometer.actors a ON a.id=s.actor_id WHERE s.workspace_id=$1 AND s.token_hash=$2 AND s.expires_at>clock_timestamp() AND m.enabled`, workspaceID, hash).Scan(&session.Member.ID, &session.Member.Name, &session.Member.Role, &session.Member.Enabled, &session.ExpiresAt)
	if err == pgx.ErrNoRows {
		return Session{}, ErrForbidden
	}
	if err != nil {
		return Session{}, ErrUnavailable
	}
	return session, nil
}

func (d *Database) RevokeSession(ctx context.Context, workspaceID, token string) error {
	hash, ok := tokenHash(token)
	if !ok {
		return ErrForbidden
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(ctx)
	var id, actorID string
	err = tx.QueryRow(ctx, `DELETE FROM gometer.sessions WHERE workspace_id=$1 AND token_hash=$2 RETURNING id,actor_id`, workspaceID, hash).Scan(&id, &actorID)
	if err == pgx.ErrNoRows {
		return ErrForbidden
	}
	if err != nil {
		return ErrUnavailable
	}
	if audit(ctx, tx, workspaceID, actorID, "session.logout", id, 0) != nil {
		return ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}

// sessionMembership reads current membership inside its caller's transaction.
func sessionMembership(ctx context.Context, tx pgx.Tx, workspaceID, issuer, subject string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT a.id FROM gometer.actors a JOIN gometer.memberships m ON m.actor_id=a.id WHERE m.workspace_id=$1 AND a.issuer=$2 AND a.subject=$3 AND m.enabled`, workspaceID, issuer, subject).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", ErrForbidden
	}
	if err != nil {
		return "", ErrUnavailable
	}
	return id, nil
}

// CheckWorkspace verifies explicit bootstrap before a team listener starts.
func (d *Database) CheckWorkspace(ctx context.Context, id string) error {
	if !ValidID(id) {
		return ErrForbidden
	}
	var ready bool
	if d.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gometer.memberships WHERE workspace_id=$1 AND role='admin' AND enabled)`, id).Scan(&ready) != nil {
		return ErrUnavailable
	}
	if !ready {
		return ErrForbidden
	}
	return nil
}
