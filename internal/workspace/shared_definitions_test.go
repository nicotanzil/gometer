package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gometer/internal/config"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func sharedJSON(ref SecretReference) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":1,"public_config":{"name":"Shared checkout","url":"http://127.0.0.1:1","headers":{"Authorization":""}},"secret_bindings":[{"path":"/headers/Authorization","reference_id":%q,"revision":%d}]}`, ref.ID, ref.Revision))
}
func TestPostgresSharedDefinitionsKeepLegacyPrivateAndConcealSources(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, operator, viewer := setupMembers(t, db)
	ref, err := db.CreateSecretReference(ctx, wid, admin, "Checkout credential", "PRIVATE_SOURCE")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := db.CreateDefinition(ctx, wid, admin, definitionJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SharedDefinition(ctx, wid, viewer, legacy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("legacy configuration became public")
	}
	if _, err = db.UpdateSharedDefinition(ctx, wid, operator, legacy.ID, 1, sharedJSON(ref)); !errors.Is(err, ErrNotFound) {
		t.Fatal("legacy configuration implicitly converted")
	}
	if _, err = db.CreateSharedDefinition(ctx, wid, viewer, sharedJSON(ref)); !errors.Is(err, ErrForbidden) {
		t.Fatal("viewer created a shared test")
	}
	saved, err := db.CreateSharedDefinition(ctx, wid, operator, sharedJSON(ref))
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{admin, operator, viewer} {
		got, err := db.SharedDefinition(ctx, wid, actor, saved.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(got)
		if !strings.Contains(string(raw), ref.ID) || !strings.Contains(string(raw), "http://127.0.0.1:1") || strings.Contains(string(raw), "PRIVATE_SOURCE") || len(got.Config) != 0 {
			t.Fatal("shared read lost public fields or disclosed a source/config")
		}
	}
	copied, err := db.CopySharedDefinition(ctx, wid, admin, saved.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if copied.ID == saved.ID || copied.Creator.ID != admin || copied.Source == nil || copied.Source.ID != saved.ID || copied.Revision != 1 || copied.Document.SecretBindings[0].Revision != 1 {
		t.Fatal("copy provenance or binding changed")
	}
	changed := []byte(strings.Replace(string(sharedJSON(ref)), "Shared checkout", "Edited checkout", 1))
	updated, err := db.UpdateSharedDefinition(ctx, wid, admin, saved.ID, 1, changed)
	if err != nil || updated.Revision != 2 || updated.Creator.ID != operator || updated.Editor.ID != admin {
		t.Fatalf("shared save: %v", err)
	}
	if _, err = db.CopySharedDefinition(ctx, wid, admin, saved.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale copy accepted")
	}
	if _, err = db.SharedDefinition(ctx, wid, viewer, copied.ID); err != nil {
		t.Fatal("copy not independently readable")
	}
	// Current role checks apply to old sessions and repository callers alike.
	if _, err = db.SetMember(ctx, wid, admin, Identity{"https://keycloak.example/realms/gometer", "operator-subject", "Operator"}, "viewer", true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateSharedDefinition(ctx, wid, operator, saved.ID, 2, changed); !errors.Is(err, ErrForbidden) {
		t.Fatal("downgraded operator wrote")
	}
	other, _ := NewID()
	_, err = db.Bootstrap(ctx, other, "Other", Identity{"https://keycloak.example/realms/gometer", "other", "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SharedDefinition(ctx, other, viewer, saved.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross-workspace read allowed")
	}
	// A raw repository edit withdraws public classification rather than leaving
	// an old shared document visible alongside new private configuration.
	if _, err = db.UpdateDefinition(ctx, wid, admin, saved.ID, 2, definitionJSON); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SharedDefinition(ctx, wid, viewer, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("raw update retained public classification")
	}
}
func TestPostgresSharedBindingRevisionAndAuditRollback(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, operator, _ := setupMembers(t, db)
	ref, err := db.CreateSecretReference(ctx, wid, admin, "Credential", "PRIVATE_SOURCE")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := db.CreateSharedDefinition(ctx, wid, operator, sharedJSON(ref))
	if err != nil {
		t.Fatal(err)
	}
	next, err := db.UpdateSecretReference(ctx, wid, admin, ref.ID, 1, ref.Name, "new-source", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CopySharedDefinition(ctx, wid, operator, saved.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("copy silently retargeted secret")
	}
	if _, err = db.UpdateSharedDefinition(ctx, wid, operator, saved.ID, 1, sharedJSON(ref)); !errors.Is(err, ErrConflict) {
		t.Fatal("stale reference accepted")
	}
	if _, err = db.CreateSharedDefinition(ctx, wid, operator, sharedJSON(SecretReference{ID: strings.Repeat("f", 32), Revision: 1})); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown source accepted")
	}
	if _, err = db.UpdateSecretReference(ctx, wid, admin, next.ID, next.Revision, next.Name, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CreateSharedDefinition(ctx, wid, operator, sharedJSON(next)); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled source accepted")
	}
	// Reading the pinned document is still safe; it does not resolve a credential.
	if got, err := db.SharedDefinition(ctx, wid, operator, saved.ID); err != nil || got.Document.SecretBindings[0].Revision != 1 {
		t.Fatal("read changed a pinned binding")
	}
	if _, err = db.pool.Exec(ctx, `CREATE FUNCTION gometer.reject_shared_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture audit failure';END$$; CREATE TRIGGER reject_shared_audit BEFORE INSERT ON gometer.audit_events FOR EACH ROW WHEN (NEW.action='test.update') EXECUTE FUNCTION gometer.reject_shared_audit()`); err != nil {
		t.Fatal(err)
	}
	public := []byte(`{"schema_version":1,"public_config":{"name":"Replacement","url":"https://target.example"},"secret_bindings":[]}`)
	if _, err = db.UpdateSharedDefinition(ctx, wid, operator, saved.ID, 1, public); !errors.Is(err, ErrUnavailable) {
		t.Fatal("save without audit succeeded")
	}
	got, err := db.SharedDefinition(ctx, wid, operator, saved.ID)
	if err != nil || got.Revision != 1 || got.Name != "Shared checkout" {
		t.Fatal("failed audit altered definition")
	}
}
func TestPostgresSharedDefinitionRevisionRace(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, operator, _ := setupMembers(t, db)
	ref, err := db.CreateSecretReference(ctx, wid, admin, "Credential", "credential")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := db.CreateSharedDefinition(ctx, wid, operator, sharedJSON(ref))
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, os.Getenv("GOMETER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, conn := range []*Database{db, other} {
		wg.Add(1)
		go func(conn *Database) {
			defer wg.Done()
			_, err := conn.UpdateSharedDefinition(ctx, wid, operator, saved.ID, 1, sharedJSON(ref))
			outcomes <- err
		}(conn)
	}
	wg.Wait()
	close(outcomes)
	success, conflict := 0, 0
	for err := range outcomes {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("two editors overwrote one another")
	}
}
func TestPostgresSharedMigrationPreservesPrivateDefinitionsSessionsAndReferences(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, _, viewer := setupMembers(t, db)
	saved, err := db.CreateDefinition(ctx, wid, admin, definitionJSON)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := db.CreateSecretReference(ctx, wid, admin, "Credential", "PRIVATE_SOURCE")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := db.NewSession(ctx, wid, "https://keycloak.example/realms/gometer", "admin-subject", time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, `DROP TABLE gometer.test_drafts; DELETE FROM gometer.schema_migrations WHERE version=5; ALTER TABLE gometer.test_definitions DROP COLUMN shared_document; DELETE FROM gometer.schema_migrations WHERE version=4`); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SharedDefinition(ctx, wid, viewer, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("migration published a legacy configuration")
	}
	if _, err = db.SecretReferenceMetadata(ctx, wid, viewer, ref.ID); err != nil {
		t.Fatal("migration lost a reference")
	}
	if _, err = db.ResolveSession(ctx, wid, token); err != nil {
		t.Fatal("migration lost a session")
	}
	if got, err := getRawForFixture(ctx, db, wid, saved.ID); err != nil || !strings.Contains(string(got.Config), "PRIVATEHEADER") {
		t.Fatal("migration lost private config")
	}
}
func getRawForFixture(ctx context.Context, db *Database, wid, id string) (Definition, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Definition{}, err
	}
	defer tx.Rollback(ctx)
	return getDefinition(ctx, tx, wid, id)
}

func TestPostgresSharedDocumentSizeBoundaryRoundTrip(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, _, operator, viewer := setupMembers(t, db)
	base := `{"schema_version":1,"public_config":{"name":"Large public body","url":"https://target.example","method":"POST","body":""},"secret_bindings":[]}`
	raw := []byte(strings.Replace(base, `"body":""`, `"body":"`+strings.Repeat("x", config.MaxConfigBytes-len(base)-1)+`"`, 1))
	if len(raw) != config.MaxConfigBytes-1 {
		t.Fatal("fixture size is incorrect")
	}
	saved, err := db.CreateSharedDefinition(ctx, wid, operator, raw)
	if err != nil {
		t.Fatal(err)
	}
	var storedSize int
	if err = db.pool.QueryRow(ctx, `SELECT octet_length(shared_document::text) FROM gometer.test_definitions WHERE workspace_id=$1 AND id=$2`, wid, saved.ID).Scan(&storedSize); err != nil || storedSize <= config.MaxConfigBytes {
		t.Fatal("fixture did not exercise PostgreSQL JSON spacing")
	}
	got, err := db.SharedDefinition(ctx, wid, viewer, saved.ID)
	if err != nil || len(got.Document.PublicConfig) < config.MaxConfigBytes-256 {
		t.Fatal("near-boundary document could not be read after PostgreSQL formatting")
	}
}
