package workspace

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

type Identity struct {
	Issuer      string
	Subject     string
	DisplayName string
}
type Member struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Enabled bool   `json:"enabled"`
}

func validText(value string, limit int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= limit && !strings.ContainsFunc(value, unicode.IsControl)
}
func validIdentity(identity Identity) bool {
	u, err := url.Parse(identity.Issuer)
	return err == nil && len(identity.Issuer) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && validText(identity.Subject, 512) && validText(identity.DisplayName, 256)
}
func validRole(role string) bool { return role == "viewer" || role == "operator" || role == "admin" }
func actor(ctx context.Context, tx pgx.Tx, identity Identity) (string, error) {
	id, err := NewID()
	if err != nil {
		return "", err
	}
	err = tx.QueryRow(ctx, `INSERT INTO gometer.actors(id,issuer,subject,display_name) VALUES($1,$2,$3,$4) ON CONFLICT(issuer,subject) DO UPDATE SET display_name=EXCLUDED.display_name RETURNING id`, id, identity.Issuer, identity.Subject, identity.DisplayName).Scan(&id)
	if err != nil {
		return "", ErrUnavailable
	}
	return id, nil
}

// Bootstrap is an explicit database-owner operation, not a login or HTTP API.
// Only an empty workspace can acquire its first admin through this operation.
func (d *Database) Bootstrap(ctx context.Context, workspaceID, name string, identity Identity) (string, error) {
	if !ValidID(workspaceID) || !validText(name, 256) || !validIdentity(identity) {
		return "", fmt.Errorf("provide a valid workspace ID, name and exact HTTPS issuer/subject identity")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.workspaces(id,name) VALUES($1,$2) ON CONFLICT(id) DO NOTHING`, workspaceID, name); err != nil {
		return "", ErrUnavailable
	}
	var ignored string
	if tx.QueryRow(ctx, `SELECT id FROM gometer.workspaces WHERE id=$1 FOR UPDATE`, workspaceID).Scan(&ignored) != nil {
		return "", ErrUnavailable
	}
	var existing int
	if tx.QueryRow(ctx, `SELECT count(*) FROM gometer.memberships WHERE workspace_id=$1`, workspaceID).Scan(&existing) != nil {
		return "", ErrUnavailable
	}
	if existing > 0 {
		return "", fmt.Errorf("workspace already initialized; membership changes require an existing administrator")
	}
	id, err := actor(ctx, tx, identity)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.memberships(workspace_id,actor_id,role) VALUES($1,$2,'admin')`, workspaceID, id); err != nil {
		return "", ErrUnavailable
	}
	if err = audit(ctx, tx, workspaceID, id, "workspace.bootstrap", workspaceID, 0); err != nil {
		return "", err
	}
	if tx.Commit(ctx) != nil {
		return "", ErrUnavailable
	}
	return id, nil
}

// require checks current persisted membership. Workspace writes hold a short row
// lock, so permission changes and definition mutations have a definite order.
func require(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, write bool) (string, error) {
	if !ValidID(workspaceID) || !ValidID(actorID) {
		return "", ErrForbidden
	}
	lock := " FOR SHARE"
	if write {
		lock = " FOR UPDATE"
	}
	var ignored string
	err := tx.QueryRow(ctx, `SELECT id FROM gometer.workspaces WHERE id=$1`+lock, workspaceID).Scan(&ignored)
	if err == pgx.ErrNoRows {
		return "", ErrForbidden
	}
	if err != nil {
		return "", ErrUnavailable
	}
	var role string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT role,enabled FROM gometer.memberships WHERE workspace_id=$1 AND actor_id=$2 FOR SHARE`, workspaceID, actorID).Scan(&role, &enabled)
	if err == pgx.ErrNoRows {
		return "", ErrForbidden
	}
	if err != nil {
		return "", ErrUnavailable
	}
	if !enabled {
		return "", ErrForbidden
	}
	if write && role == "viewer" {
		return "", ErrForbidden
	}
	return role, nil
}
func audit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, id string, revision int64) error {
	if _, err := tx.Exec(ctx, `INSERT INTO gometer.audit_events(workspace_id,actor_id,action,entity_id,revision) VALUES($1,$2,$3,$4,NULLIF($5,0))`, workspaceID, actorID, action, id, revision); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (d *Database) SetMember(ctx context.Context, workspaceID, adminID string, identity Identity, role string, enabled bool) (string, error) {
	if !validIdentity(identity) || !validRole(role) {
		return "", fmt.Errorf("invalid member identity or role")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback(ctx)
	current, err := require(ctx, tx, workspaceID, adminID, true)
	if err != nil {
		return "", err
	}
	if current != "admin" {
		return "", ErrForbidden
	}
	id, err := actor(ctx, tx, identity)
	if err != nil {
		return "", err
	}
	var previousRole string
	var previousEnabled bool
	previousErr := tx.QueryRow(ctx, `SELECT role,enabled FROM gometer.memberships WHERE workspace_id=$1 AND actor_id=$2`, workspaceID, id).Scan(&previousRole, &previousEnabled)
	if previousErr != nil && previousErr != pgx.ErrNoRows {
		return "", ErrUnavailable
	}
	if previousRole == "admin" && previousEnabled && (!enabled || role != "admin") {
		var count int
		if tx.QueryRow(ctx, `SELECT count(*) FROM gometer.memberships WHERE workspace_id=$1 AND role='admin' AND enabled`, workspaceID).Scan(&count) != nil {
			return "", ErrUnavailable
		}
		if count <= 1 {
			return "", fmt.Errorf("cannot remove the workspace's last enabled administrator")
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.memberships(workspace_id,actor_id,role,enabled) VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,actor_id) DO UPDATE SET role=EXCLUDED.role,enabled=EXCLUDED.enabled,updated_at=clock_timestamp()`, workspaceID, id, role, enabled); err != nil {
		return "", ErrUnavailable
	}
	if audit(ctx, tx, workspaceID, adminID, "membership.change", id, 0) != nil {
		return "", ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return "", ErrUnavailable
	}
	return id, nil
}

// ResolveMember accepts an identity already verified by OIDC. It never grants
// membership from an email address, username, forwarded header, or LDAP password.
func (d *Database) ResolveMember(ctx context.Context, workspaceID, issuer, subject string) (Member, error) {
	var m Member
	err := d.pool.QueryRow(ctx, `SELECT a.id,a.display_name,m.role,m.enabled FROM gometer.actors a JOIN gometer.memberships m ON m.actor_id=a.id WHERE m.workspace_id=$1 AND a.issuer=$2 AND a.subject=$3`, workspaceID, issuer, subject).Scan(&m.ID, &m.Name, &m.Role, &m.Enabled)
	if err == pgx.ErrNoRows {
		return m, ErrForbidden
	}
	if err != nil {
		return m, ErrUnavailable
	}
	if !m.Enabled {
		return Member{}, ErrForbidden
	}
	return m, nil
}
