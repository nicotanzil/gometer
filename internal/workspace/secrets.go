package workspace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gometer/internal/secretfiles"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const maxSecretReferences = 200

// SecretReference is the public catalog projection. Source keys and resolved
// values are deliberately absent, including for workspace administrators.
type SecretReference struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Revision  int64     `json:"revision"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const secretColumns = `id,name,revision,enabled,created_at,updated_at`

func scanSecret(row pgx.Row) (SecretReference, error) {
	var ref SecretReference
	err := row.Scan(&ref.ID, &ref.Name, &ref.Revision, &ref.Enabled, &ref.CreatedAt, &ref.UpdatedAt)
	if err == pgx.ErrNoRows {
		return ref, ErrNotFound
	}
	if err != nil {
		return ref, ErrUnavailable
	}
	return ref, nil
}
func secretWriteError(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return ErrConflict
	}
	return ErrUnavailable
}
func requireAdmin(ctx context.Context, tx pgx.Tx, wid, actorID string) error {
	role, err := require(ctx, tx, wid, actorID, true)
	if err != nil {
		return err
	}
	if role != "admin" {
		return ErrForbidden
	}
	return nil
}
func (d *Database) CreateSecretReference(ctx context.Context, wid, adminID, name, key string) (SecretReference, error) {
	if !validText(name, 256) || !secretfiles.ValidKey(key) {
		return SecretReference{}, fmt.Errorf("provide a public reference name and a valid mounted-file key")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return SecretReference{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if err = requireAdmin(ctx, tx, wid, adminID); err != nil {
		return SecretReference{}, err
	}
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM gometer.secret_references WHERE workspace_id=$1`, wid).Scan(&count) != nil {
		return SecretReference{}, ErrUnavailable
	}
	if count >= maxSecretReferences {
		return SecretReference{}, fmt.Errorf("workspace secret reference catalog is full (200)")
	}
	id, err := NewID()
	if err != nil {
		return SecretReference{}, ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.secret_references(workspace_id,id,name,source_key,revision,created_by,last_edited_by) VALUES($1,$2,$3,$4,1,$5,$5)`, wid, id, name, key, adminID); err != nil {
		return SecretReference{}, secretWriteError(err)
	}
	ref, err := scanSecret(tx.QueryRow(ctx, `SELECT `+secretColumns+` FROM gometer.secret_references WHERE workspace_id=$1 AND id=$2`, wid, id))
	if err != nil {
		return SecretReference{}, err
	}
	if audit(ctx, tx, wid, adminID, "secret.create", id, 1) != nil {
		return SecretReference{}, ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return SecretReference{}, ErrUnavailable
	}
	return ref, nil
}

// UpdateSecretReference keeps the existing hidden source when key is empty.
func (d *Database) UpdateSecretReference(ctx context.Context, wid, adminID, id string, revision int64, name, key string, enabled bool) (SecretReference, error) {
	if !ValidID(id) || revision < 1 || !validText(name, 256) || (key != "" && !secretfiles.ValidKey(key)) {
		return SecretReference{}, fmt.Errorf("provide a valid reference ID, revision, public name and mounted-file key")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return SecretReference{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if err = requireAdmin(ctx, tx, wid, adminID); err != nil {
		return SecretReference{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE gometer.secret_references SET name=$3,source_key=coalesce(nullif($4,''),source_key),enabled=$5,revision=revision+1,last_edited_by=$6,updated_at=clock_timestamp() WHERE workspace_id=$1 AND id=$2 AND revision=$7`, wid, id, name, key, enabled, adminID, revision)
	if err != nil {
		return SecretReference{}, secretWriteError(err)
	}
	if tag.RowsAffected() == 0 {
		if _, err = scanSecret(tx.QueryRow(ctx, `SELECT `+secretColumns+` FROM gometer.secret_references WHERE workspace_id=$1 AND id=$2`, wid, id)); err != nil {
			return SecretReference{}, err
		}
		return SecretReference{}, ErrConflict
	}
	ref, err := scanSecret(tx.QueryRow(ctx, `SELECT `+secretColumns+` FROM gometer.secret_references WHERE workspace_id=$1 AND id=$2`, wid, id))
	if err != nil {
		return SecretReference{}, err
	}
	if audit(ctx, tx, wid, adminID, "secret.update", id, ref.Revision) != nil {
		return SecretReference{}, ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return SecretReference{}, ErrUnavailable
	}
	return ref, nil
}
func (d *Database) ListSecretReferences(ctx context.Context, wid, actorID string) ([]SecretReference, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, false); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+secretColumns+` FROM gometer.secret_references WHERE workspace_id=$1 ORDER BY lower(name),id LIMIT 200`, wid)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	list := []SecretReference{}
	for rows.Next() {
		ref, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, ref)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	rows.Close()
	if tx.Commit(ctx) != nil {
		return nil, ErrUnavailable
	}
	return list, nil
}
func (d *Database) SecretReferenceMetadata(ctx context.Context, wid, actorID, id string) (SecretReference, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return SecretReference{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, false); err != nil {
		return SecretReference{}, err
	}
	ref, err := scanSecret(tx.QueryRow(ctx, `SELECT `+secretColumns+` FROM gometer.secret_references WHERE workspace_id=$1 AND id=$2`, wid, id))
	if err != nil {
		return SecretReference{}, err
	}
	if tx.Commit(ctx) != nil {
		return SecretReference{}, ErrUnavailable
	}
	return ref, nil
}

// SecretSourceKey is an executor boundary, not a browser/export API. The caller
// must supply an exact catalog revision; disabled, stale or viewer access fails.
// It returns a filename descriptor only, never a secret value or filesystem path.
func (d *Database) SecretSourceKey(ctx context.Context, wid, actorID, id string, revision int64) (string, error) {
	if !ValidID(id) || revision < 1 {
		return "", ErrNotFound
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback(ctx)
	role, err := require(ctx, tx, wid, actorID, false)
	if err != nil {
		return "", err
	}
	if role == "viewer" {
		return "", ErrForbidden
	}
	var key string
	var current int64
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT source_key,revision,enabled FROM gometer.secret_references WHERE workspace_id=$1 AND id=$2`, wid, id).Scan(&key, &current, &enabled)
	if err == pgx.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", ErrUnavailable
	}
	if !enabled {
		return "", ErrForbidden
	}
	if current != revision {
		return "", ErrConflict
	}
	if tx.Commit(ctx) != nil {
		return "", ErrUnavailable
	}
	return key, nil
}
