package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gometer/internal/config"

	"github.com/jackc/pgx/v5"
)

const maxDefinitions = 200

// Attribution describes authorship, not the actor's current membership state.
type Attribution struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Definition struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Revision  int64             `json:"revision"`
	Creator   Attribution       `json:"creator"`
	Editor    Attribution       `json:"editor"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Source    *DefinitionSource `json:"source,omitempty"`
	// Raw configuration is intentionally excluded from automatic JSON encoding.
	// Team read/export policy must explicitly authorize any future raw-config API.
	Config json.RawMessage `json:"-"`
}
type DefinitionSource struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

func validatedConfig(raw json.RawMessage) (string, []byte, error) {
	c, err := config.Load(bytes.NewReader(raw))
	if err != nil {
		return "", nil, fmt.Errorf("test configuration is invalid")
	}
	if _, err = c.Validate(); err != nil {
		return "", nil, fmt.Errorf("test configuration is invalid")
	}
	data, err := json.Marshal(c)
	if err != nil || len(data) > config.MaxConfigBytes {
		return "", nil, fmt.Errorf("test configuration exceeds the storage bound")
	}
	return c.Name, data, nil
}

const columns = `t.id,t.name,t.revision,t.config,t.created_by,c.display_name,t.last_edited_by,e.display_name,t.created_at,t.updated_at,t.source_id,t.source_revision`
const definitionFrom = ` FROM gometer.test_definitions t JOIN gometer.actors c ON c.id=t.created_by JOIN gometer.actors e ON e.id=t.last_edited_by `

func scanDefinition(row pgx.Row) (Definition, error) {
	var d Definition
	var sourceID *string
	var revision *int64
	err := row.Scan(&d.ID, &d.Name, &d.Revision, &d.Config, &d.Creator.ID, &d.Creator.Name, &d.Editor.ID, &d.Editor.Name, &d.CreatedAt, &d.UpdatedAt, &sourceID, &revision)
	if err == pgx.ErrNoRows {
		return d, ErrNotFound
	}
	if err != nil {
		return d, ErrUnavailable
	}
	if sourceID != nil && revision != nil {
		d.Source = &DefinitionSource{ID: *sourceID, Revision: *revision}
	}
	return d, nil
}
func getDefinition(ctx context.Context, tx pgx.Tx, workspaceID, id string) (Definition, error) {
	return scanDefinition(tx.QueryRow(ctx, `SELECT `+columns+definitionFrom+` WHERE t.workspace_id=$1 AND t.id=$2`, workspaceID, id))
}

func (d *Database) CreateDefinition(ctx context.Context, workspaceID, actorID string, raw json.RawMessage) (Definition, error) {
	name, data, err := validatedConfig(raw)
	if err != nil {
		return Definition{}, err
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Definition{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, workspaceID, actorID, true); err != nil {
		return Definition{}, err
	}
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM gometer.test_definitions WHERE workspace_id=$1`, workspaceID).Scan(&count) != nil {
		return Definition{}, ErrUnavailable
	}
	if count >= maxDefinitions {
		return Definition{}, fmt.Errorf("workspace test library is full (200)")
	}
	id, err := NewID()
	if err != nil {
		return Definition{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.test_definitions(workspace_id,id,name,revision,config,created_by,last_edited_by) VALUES($1,$2,$3,1,$4,$5,$5)`, workspaceID, id, name, json.RawMessage(data), actorID); err != nil {
		return Definition{}, ErrUnavailable
	}
	if audit(ctx, tx, workspaceID, actorID, "test.create", id, 1) != nil {
		return Definition{}, ErrUnavailable
	}
	saved, err := getDefinition(ctx, tx, workspaceID, id)
	if err != nil {
		return Definition{}, err
	}
	if tx.Commit(ctx) != nil {
		return Definition{}, ErrUnavailable
	}
	return saved, nil
}

func (d *Database) UpdateDefinition(ctx context.Context, workspaceID, actorID, id string, revision int64, raw json.RawMessage) (Definition, error) {
	if !ValidID(id) || revision < 1 {
		return Definition{}, fmt.Errorf("provide a valid test ID and base revision")
	}
	name, data, err := validatedConfig(raw)
	if err != nil {
		return Definition{}, err
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Definition{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, workspaceID, actorID, true); err != nil {
		return Definition{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE gometer.test_definitions SET name=$3,config=$4,shared_document=NULL,revision=revision+1,last_edited_by=$5,updated_at=clock_timestamp() WHERE workspace_id=$1 AND id=$2 AND revision=$6`, workspaceID, id, name, json.RawMessage(data), actorID, revision)
	if err != nil {
		return Definition{}, ErrUnavailable
	}
	if tag.RowsAffected() == 0 {
		if _, err := getDefinition(ctx, tx, workspaceID, id); err != nil {
			return Definition{}, err
		}
		return Definition{}, ErrConflict
	}
	saved, err := getDefinition(ctx, tx, workspaceID, id)
	if err != nil {
		return Definition{}, err
	}
	if audit(ctx, tx, workspaceID, actorID, "test.update", id, saved.Revision) != nil {
		return Definition{}, ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return Definition{}, ErrUnavailable
	}
	return saved, nil
}

func (d *Database) CopyDefinition(ctx context.Context, workspaceID, actorID, id string, revision int64) (Definition, error) {
	if !ValidID(id) || revision < 1 {
		return Definition{}, fmt.Errorf("provide a valid test ID and base revision")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Definition{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, workspaceID, actorID, true); err != nil {
		return Definition{}, err
	}
	source, err := getDefinition(ctx, tx, workspaceID, id)
	if err != nil {
		return Definition{}, err
	}
	if source.Revision != revision {
		return Definition{}, ErrConflict
	}
	var count int
	if tx.QueryRow(ctx, `SELECT count(*) FROM gometer.test_definitions WHERE workspace_id=$1`, workspaceID).Scan(&count) != nil {
		return Definition{}, ErrUnavailable
	}
	if count >= maxDefinitions {
		return Definition{}, fmt.Errorf("workspace test library is full (200)")
	}
	newID, err := NewID()
	if err != nil {
		return Definition{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gometer.test_definitions(workspace_id,id,name,revision,config,created_by,last_edited_by,source_id,source_revision) VALUES($1,$2,$3,1,$4,$5,$5,$6,$7)`, workspaceID, newID, source.Name, source.Config, actorID, id, revision); err != nil {
		return Definition{}, ErrUnavailable
	}
	if audit(ctx, tx, workspaceID, actorID, "test.copy", newID, 1) != nil {
		return Definition{}, ErrUnavailable
	}
	saved, err := getDefinition(ctx, tx, workspaceID, newID)
	if err != nil {
		return Definition{}, err
	}
	if tx.Commit(ctx) != nil {
		return Definition{}, ErrUnavailable
	}
	return saved, nil
}

func (d *Database) ListDefinitions(ctx context.Context, workspaceID, actorID, query string, mine bool) ([]Definition, error) {
	if len(query) > 256 {
		return nil, fmt.Errorf("search is limited to 256 bytes")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, workspaceID, actorID, false); err != nil {
		return nil, err
	}
	pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query) + "%"
	rows, err := tx.Query(ctx, `SELECT `+strings.Replace(columns, "t.config", "NULL::jsonb", 1)+definitionFrom+` WHERE t.workspace_id=$1 AND (NOT $2 OR t.created_by=$3) AND (t.name ILIKE $4 OR c.display_name ILIKE $4 OR e.display_name ILIKE $4) ORDER BY t.updated_at DESC,t.id LIMIT 200`, workspaceID, mine, actorID, pattern)
	if err != nil {
		return nil, ErrUnavailable
	}
	list := []Definition{}
	for rows.Next() {
		item, err := scanDefinition(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		item.Config = nil
		list = append(list, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return nil, ErrUnavailable
	}
	return list, nil
}

// DefinitionMetadata never selects raw configuration, even for administrators.
func (d *Database) DefinitionMetadata(ctx context.Context, wid, actorID, id string) (Definition, error) {
	if !ValidID(id) {
		return Definition{}, ErrNotFound
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Definition{}, ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = require(ctx, tx, wid, actorID, false); err != nil {
		return Definition{}, err
	}
	item, err := scanDefinition(tx.QueryRow(ctx, `SELECT `+strings.Replace(columns, "t.config", "NULL::jsonb", 1)+definitionFrom+` WHERE t.workspace_id=$1 AND t.id=$2`, wid, id))
	if err != nil {
		return Definition{}, err
	}
	if tx.Commit(ctx) != nil {
		return Definition{}, ErrUnavailable
	}
	return item, nil
}
