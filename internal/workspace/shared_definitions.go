package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gometer/internal/config"
	"gometer/internal/sharedtest"

	"github.com/jackc/pgx/v5"
)

// SharedDefinition carries only deliberately public fields and reference IDs.
// Legacy definitions have no document and cannot be read through this boundary.
type SharedDefinition struct {
	Definition
	Document sharedtest.Document `json:"document"`
}

func getSharedDefinition(ctx context.Context, tx pgx.Tx, wid, id string) (SharedDefinition, error) {
	meta, err := scanDefinition(tx.QueryRow(ctx, `SELECT `+strings.Replace(columns, "t.config", "NULL::jsonb", 1)+definitionFrom+` WHERE t.workspace_id=$1 AND t.id=$2 AND t.shared_document IS NOT NULL`, wid, id))
	if err != nil {
		return SharedDefinition{}, err
	}
	var raw []byte
	if tx.QueryRow(ctx, `SELECT shared_document FROM gometer.test_definitions WHERE workspace_id=$1 AND id=$2`, wid, id).Scan(&raw) != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	doc, _, err := sharedtest.Parse(compact.Bytes())
	if err != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	return SharedDefinition{Definition: meta, Document: doc}, nil
}

func (d *Database) SharedDefinition(ctx context.Context, wid, actorID, id string) (SharedDefinition, error) {
	if !ValidID(id) {
		return SharedDefinition{}, ErrNotFound
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, false); err != nil {
		return SharedDefinition{}, err
	}
	saved, err := getSharedDefinition(ctx, tx, wid, id)
	if err != nil {
		return SharedDefinition{}, err
	}
	if tx.Commit(ctx) != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	return saved, nil
}

// validateBindings runs under the workspace write lock. Reference changes and
// definition saves therefore have a definite order; a stale binding never moves
// silently to a different mounted source. No source key or secret is selected.
func validateBindings(ctx context.Context, tx pgx.Tx, wid string, doc sharedtest.Document) error {
	checked := map[string]int64{}
	for _, binding := range doc.SecretBindings {
		if current, ok := checked[binding.ReferenceID]; ok {
			if current != binding.Revision {
				return ErrConflict
			}
			continue
		}
		var revision int64
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT revision,enabled FROM gometer.secret_references WHERE workspace_id=$1 AND id=$2`, wid, binding.ReferenceID).Scan(&revision, &enabled)
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return ErrUnavailable
		}
		if !enabled {
			return ErrForbidden
		}
		if revision != binding.Revision {
			return ErrConflict
		}
		checked[binding.ReferenceID] = revision
	}
	return nil
}

func (d *Database) CreateSharedDefinition(ctx context.Context, wid, actorID string, raw []byte) (SharedDefinition, error) {
	return d.saveSharedDefinition(ctx, wid, actorID, "", 0, raw)
}
func (d *Database) UpdateSharedDefinition(ctx context.Context, wid, actorID, id string, revision int64, raw []byte) (SharedDefinition, error) {
	if !ValidID(id) || revision < 1 {
		return SharedDefinition{}, sharedtest.ErrInvalid
	}
	return d.saveSharedDefinition(ctx, wid, actorID, id, revision, raw)
}
func (d *Database) saveSharedDefinition(ctx context.Context, wid, actorID, id string, revision int64, raw []byte) (SharedDefinition, error) {
	doc, c, err := sharedtest.Parse(raw)
	if err != nil || !validText(c.Name, 256) {
		return SharedDefinition{}, sharedtest.ErrInvalid
	}
	encoded, err := json.Marshal(doc)
	if err != nil || len(encoded) > config.MaxConfigBytes {
		return SharedDefinition{}, sharedtest.ErrInvalid
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, true); err != nil {
		return SharedDefinition{}, err
	}
	if err = validateBindings(ctx, tx, wid, doc); err != nil {
		return SharedDefinition{}, err
	}
	action := "test.update"
	if id == "" {
		if err = definitionCapacity(ctx, tx, wid); err != nil {
			return SharedDefinition{}, err
		}
		id, err = NewID()
		if err != nil {
			return SharedDefinition{}, ErrUnavailable
		}
		if _, err = tx.Exec(ctx, `INSERT INTO gometer.test_definitions(workspace_id,id,name,revision,config,shared_document,created_by,last_edited_by) VALUES($1,$2,$3,1,$4,$5,$6,$6)`, wid, id, c.Name, doc.PublicConfig, json.RawMessage(encoded), actorID); err != nil {
			return SharedDefinition{}, ErrUnavailable
		}
		action = "test.create"
	} else {
		tag, err := tx.Exec(ctx, `UPDATE gometer.test_definitions SET name=$3,config=$4,shared_document=$5,revision=revision+1,last_edited_by=$6,updated_at=clock_timestamp() WHERE workspace_id=$1 AND id=$2 AND revision=$7 AND shared_document IS NOT NULL`, wid, id, c.Name, doc.PublicConfig, json.RawMessage(encoded), actorID, revision)
		if err != nil {
			return SharedDefinition{}, ErrUnavailable
		}
		if tag.RowsAffected() == 0 {
			if _, err = getSharedDefinition(ctx, tx, wid, id); err != nil {
				return SharedDefinition{}, err
			}
			return SharedDefinition{}, ErrConflict
		}
	}
	saved, err := getSharedDefinition(ctx, tx, wid, id)
	if err != nil {
		return SharedDefinition{}, err
	}
	if audit(ctx, tx, wid, actorID, action, id, saved.Revision) != nil || tx.Commit(ctx) != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	return saved, nil
}

func definitionCapacity(ctx context.Context, tx pgx.Tx, wid string) error {
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM gometer.test_definitions WHERE workspace_id=$1`, wid).Scan(&count) != nil {
		return ErrUnavailable
	}
	if count >= maxDefinitions {
		return fmt.Errorf("workspace test library is full (200)")
	}
	return nil
}

func (d *Database) CopySharedDefinition(ctx context.Context, wid, actorID, id string, revision int64) (SharedDefinition, error) {
	if !ValidID(id) || revision < 1 {
		return SharedDefinition{}, sharedtest.ErrInvalid
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, true); err != nil {
		return SharedDefinition{}, err
	}
	source, err := getSharedDefinition(ctx, tx, wid, id)
	if err != nil {
		return SharedDefinition{}, err
	}
	if source.Revision != revision {
		return SharedDefinition{}, ErrConflict
	}
	if err = validateBindings(ctx, tx, wid, source.Document); err != nil {
		return SharedDefinition{}, err
	}
	if err = definitionCapacity(ctx, tx, wid); err != nil {
		return SharedDefinition{}, err
	}
	newID, err := NewID()
	if err != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	encoded, err := json.Marshal(source.Document)
	if err != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.test_definitions(workspace_id,id,name,revision,config,shared_document,created_by,last_edited_by,source_id,source_revision) VALUES($1,$2,$3,1,$4,$5,$6,$6,$7,$8)`, wid, newID, source.Name, source.Document.PublicConfig, json.RawMessage(encoded), actorID, id, revision); err != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	saved, err := getSharedDefinition(ctx, tx, wid, newID)
	if err != nil {
		return SharedDefinition{}, err
	}
	if audit(ctx, tx, wid, actorID, "test.copy", newID, 1) != nil || tx.Commit(ctx) != nil {
		return SharedDefinition{}, ErrUnavailable
	}
	return saved, nil
}
