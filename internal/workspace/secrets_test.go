package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresSecretReferencesConcealSourcesAndRecheckRoles(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, operator, viewer := setupMembers(t, db)
	for _, actor := range []string{operator, viewer} {
		if _, err := db.CreateSecretReference(ctx, wid, actor, "Checkout token", "PRIVATE_SOURCE_KEY"); !errors.Is(err, ErrForbidden) {
			t.Fatal("non-admin created reference")
		}
	}
	ref, err := db.CreateSecretReference(ctx, wid, admin, "Checkout token", "PRIVATE_SOURCE_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Revision != 1 || !ref.Enabled {
		t.Fatal("invalid new reference metadata")
	}
	for _, actor := range []string{admin, operator, viewer} {
		list, err := db.ListSecretReferences(ctx, wid, actor)
		if err != nil || len(list) != 1 || list[0].ID != ref.ID {
			t.Fatal("catalog discovery failed")
		}
		data, _ := json.Marshal(list)
		if strings.Contains(string(data), "PRIVATE_SOURCE_KEY") || strings.Contains(string(data), "source_key") {
			t.Fatal("metadata exposed a source")
		}
	}
	if _, err = db.SecretSourceKey(ctx, wid, viewer, ref.ID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatal("viewer obtained executor source")
	}
	key, err := db.SecretSourceKey(ctx, wid, operator, ref.ID, 1)
	if err != nil || key != "PRIVATE_SOURCE_KEY" {
		t.Fatal("authorized source descriptor lookup failed")
	}
	if _, err = db.CreateSecretReference(ctx, wid, admin, "checkout TOKEN", "other-key"); !errors.Is(err, ErrConflict) {
		t.Fatal("case-insensitive duplicate alias accepted")
	}
	changed, err := db.UpdateSecretReference(ctx, wid, admin, ref.ID, 1, "Checkout token", "PRIVATE_NEW_SOURCE", true)
	if err != nil || changed.ID != ref.ID || changed.Revision != 2 {
		t.Fatal("reference update changed identity or revision")
	}
	if key, err := db.SecretSourceKey(ctx, wid, operator, ref.ID, 2); err != nil || key != "PRIVATE_NEW_SOURCE" {
		t.Fatal("updated reference returned the wrong source descriptor")
	}
	if _, err = db.SecretSourceKey(ctx, wid, operator, ref.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale source silently retargeted")
	}
	if _, err = db.UpdateSecretReference(ctx, wid, admin, ref.ID, 1, "Stale", "other-key", false); !errors.Is(err, ErrConflict) {
		t.Fatal("stale catalog write accepted")
	}
	if _, err = db.UpdateSecretReference(ctx, wid, admin, ref.ID, 2, "Checkout token", "PRIVATE_NEW_SOURCE", false); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SecretSourceKey(ctx, wid, operator, ref.ID, 3); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled reference resolved")
	}
	other, _ := NewID()
	if _, err = db.Bootstrap(ctx, other, "Other", Identity{"https://keycloak.example/realms/gometer", "other", "Other"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SecretReferenceMetadata(ctx, other, operator, ref.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross-workspace metadata allowed")
	}
	var leak bool
	if db.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gometer.audit_events WHERE entity_id='PRIVATE_SOURCE_KEY' OR action LIKE '%PRIVATE%')`).Scan(&leak) != nil || leak {
		t.Fatal("audit leaked source details")
	}
}
func TestPostgresSecretRevisionRaceAndAuditRollback(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, _, _ := setupMembers(t, db)
	ref, err := db.CreateSecretReference(ctx, wid, admin, "Token", "token")
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
	for _, connection := range []*Database{db, other} {
		wg.Add(1)
		go func(connection *Database) {
			defer wg.Done()
			_, err := connection.UpdateSecretReference(ctx, wid, admin, ref.ID, 1, "Token", "rotated", true)
			outcomes <- err
		}(connection)
	}
	wg.Wait()
	close(outcomes)
	successes, conflicts := 0, 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("catalog update race accepted two saves")
	}
	_, err = db.pool.Exec(ctx, `CREATE FUNCTION gometer.reject_secret_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture audit failure';END$$; CREATE TRIGGER reject_secret_audit BEFORE INSERT ON gometer.audit_events FOR EACH ROW WHEN (NEW.action='secret.update') EXECUTE FUNCTION gometer.reject_secret_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateSecretReference(ctx, wid, admin, ref.ID, 2, "Changed", "changed", false); !errors.Is(err, ErrUnavailable) {
		t.Fatal("reference accepted without audit")
	}
	actual, err := db.SecretReferenceMetadata(ctx, wid, admin, ref.ID)
	if err != nil || actual.Revision != 2 || actual.Name != "Token" || !actual.Enabled {
		t.Fatal("failed audit changed catalog")
	}
}

func TestPostgresSecretMigrationPreservesDefinitionsAndSessions(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, _, _ := setupMembers(t, db)
	saved, err := db.CreateDefinition(ctx, wid, admin, definitionJSON)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := db.NewSession(ctx, wid, "https://keycloak.example/realms/gometer", "admin-subject", time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the preceding committed schema without changing migration 001/002.
	if _, err = db.pool.Exec(ctx, `DROP TABLE gometer.test_drafts; DELETE FROM gometer.schema_migrations WHERE version=5; ALTER TABLE gometer.test_definitions DROP COLUMN shared_document; DELETE FROM gometer.schema_migrations WHERE version=4; DROP TABLE gometer.secret_references; DELETE FROM gometer.schema_migrations WHERE version=3`); err != nil {
		t.Fatal(err)
	}
	if err = db.Check(ctx); !errors.Is(err, ErrSchema) {
		t.Fatal("old schema reported ready")
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if session, err := db.ResolveSession(ctx, wid, token); err != nil || session.Member.ID != admin {
		t.Fatal("secret migration lost an active session")
	}
	actual, err := db.DefinitionMetadata(ctx, wid, admin, saved.ID)
	if err != nil || actual.Revision != saved.Revision || actual.Creator.ID != admin || len(actual.Config) != 0 {
		t.Fatal("secret migration changed existing definition or fetched raw configuration")
	}
}
