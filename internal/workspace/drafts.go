package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"gometer/internal/config"
	"gometer/internal/sharedtest"
)

const MaxDrafts = 10

type Draft struct {
	ID        string               `json:"id"`
	Revision  int64                `json:"revision"`
	Name      string               `json:"name"`
	Base      *DefinitionSource    `json:"base,omitempty"`
	UpdatedAt time.Time            `json:"updated_at"`
	Document  *sharedtest.Document `json:"document,omitempty"`
}

func scanDraft(row pgx.Row) (Draft, error) {
	var draft Draft
	var baseID *string
	var baseRevision *int64
	var raw []byte
	err := row.Scan(&draft.ID, &draft.Revision, &draft.Name, &baseID, &baseRevision, &draft.UpdatedAt, &raw)
	if err == pgx.ErrNoRows {
		return draft, ErrNotFound
	}
	if err != nil {
		return draft, ErrUnavailable
	}
	if baseID != nil && baseRevision != nil {
		draft.Base = &DefinitionSource{ID: *baseID, Revision: *baseRevision}
	}
	if raw != nil {
		var compact bytes.Buffer
		if json.Compact(&compact, raw) != nil {
			return Draft{}, ErrUnavailable
		}
		doc, _, err := sharedtest.Parse(compact.Bytes())
		if err != nil {
			return Draft{}, ErrUnavailable
		}
		draft.Document = &doc
	}
	return draft, nil
}

const draftColumns = `id,revision,name,base_test_id,base_revision,updated_at,document`

func getDraft(ctx context.Context, tx pgx.Tx, wid, actorID, id string) (Draft, error) {
	return scanDraft(tx.QueryRow(ctx, `SELECT `+draftColumns+` FROM gometer.test_drafts WHERE workspace_id=$1 AND actor_id=$2 AND id=$3`, wid, actorID, id))
}
func (d *Database) ListDrafts(ctx context.Context, wid, actorID string) ([]Draft, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, false); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,revision,name,base_test_id,base_revision,updated_at,NULL::jsonb FROM gometer.test_drafts WHERE workspace_id=$1 AND actor_id=$2 ORDER BY updated_at DESC,id LIMIT 10`, wid, actorID)
	if err != nil {
		return nil, ErrUnavailable
	}
	drafts := []Draft{}
	for rows.Next() {
		draft, err := scanDraft(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || tx.Commit(ctx) != nil {
		return nil, ErrUnavailable
	}
	return drafts, nil
}
func (d *Database) Draft(ctx context.Context, wid, actorID, id string) (Draft, error) {
	if !ValidID(id) {
		return Draft{}, ErrNotFound
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Draft{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, false); err != nil {
		return Draft{}, err
	}
	draft, err := getDraft(ctx, tx, wid, actorID, id)
	if err != nil {
		return Draft{}, err
	}
	if tx.Commit(ctx) != nil {
		return Draft{}, ErrUnavailable
	}
	return draft, nil
}

// SaveDraft checks classification, not current reference availability. A pinned
// reference can become stale while editing; a draft must remain recoverable.
// Publishing a definition separately checks current enabled reference revisions.
func (d *Database) SaveDraft(ctx context.Context, wid, actorID, id string, revision int64, base *DefinitionSource, raw []byte) (Draft, error) {
	if id != "" && (!ValidID(id) || revision < 1) || id == "" && revision != 0 || base != nil && (!ValidID(base.ID) || base.Revision < 1) {
		return Draft{}, sharedtest.ErrInvalid
	}
	doc, c, err := sharedtest.Parse(raw)
	if err != nil || !validText(c.Name, 256) {
		return Draft{}, sharedtest.ErrInvalid
	}
	encoded, err := json.Marshal(doc)
	if err != nil || len(encoded) > config.MaxConfigBytes {
		return Draft{}, sharedtest.ErrInvalid
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Draft{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, true); err != nil {
		return Draft{}, err
	}
	var baseID any
	var baseRevision any
	if base != nil {
		// Require a classified source in this workspace, but retain its old revision
		// for honest conflict detection after another author saves a newer revision.
		source, err := getSharedDefinition(ctx, tx, wid, base.ID)
		if err != nil {
			return Draft{}, err
		}
		if base.Revision > source.Revision {
			return Draft{}, ErrConflict
		}
		baseID, baseRevision = base.ID, base.Revision
	}
	if id == "" {
		var count int
		if tx.QueryRow(ctx, `SELECT count(*) FROM gometer.test_drafts WHERE workspace_id=$1 AND actor_id=$2`, wid, actorID).Scan(&count) != nil {
			return Draft{}, ErrUnavailable
		}
		if count >= MaxDrafts {
			return Draft{}, ErrDraftLimit
		}
		id, err = NewID()
		if err != nil {
			return Draft{}, ErrUnavailable
		}
		if _, err = tx.Exec(ctx, `INSERT INTO gometer.test_drafts(workspace_id,actor_id,id,revision,name,base_test_id,base_revision,document) VALUES($1,$2,$3,1,$4,$5,$6,$7)`, wid, actorID, id, c.Name, baseID, baseRevision, json.RawMessage(encoded)); err != nil {
			return Draft{}, ErrUnavailable
		}
	} else {
		tag, err := tx.Exec(ctx, `UPDATE gometer.test_drafts SET revision=revision+1,name=$4,base_test_id=$5,base_revision=$6,document=$7,updated_at=clock_timestamp() WHERE workspace_id=$1 AND actor_id=$2 AND id=$3 AND revision=$8`, wid, actorID, id, c.Name, baseID, baseRevision, json.RawMessage(encoded), revision)
		if err != nil {
			return Draft{}, ErrUnavailable
		}
		if tag.RowsAffected() == 0 {
			if _, err = getDraft(ctx, tx, wid, actorID, id); err != nil {
				return Draft{}, err
			}
			return Draft{}, ErrConflict
		}
	}
	saved, err := getDraft(ctx, tx, wid, actorID, id)
	if err != nil {
		return Draft{}, err
	}
	if audit(ctx, tx, wid, actorID, "draft.save", id, saved.Revision) != nil || tx.Commit(ctx) != nil {
		return Draft{}, ErrUnavailable
	}
	return saved, nil
}
func (d *Database) DeleteDraft(ctx context.Context, wid, actorID, id string, revision int64) error {
	if !ValidID(id) || revision < 1 {
		return sharedtest.ErrInvalid
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, true); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM gometer.test_drafts WHERE workspace_id=$1 AND actor_id=$2 AND id=$3 AND revision=$4`, wid, actorID, id, revision)
	if err != nil {
		return ErrUnavailable
	}
	if tag.RowsAffected() == 0 {
		if _, err = getDraft(ctx, tx, wid, actorID, id); err != nil {
			return err
		}
		return ErrConflict
	}
	if audit(ctx, tx, wid, actorID, "draft.delete", id, revision) != nil || tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}
