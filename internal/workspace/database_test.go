package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Integration tests may only modify a dedicated loopback test database.
func fixture(t *testing.T) *Database {
	t.Helper()
	dsn := os.Getenv("GOMETER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GOMETER_TEST_DATABASE_URL for isolated PostgreSQL integration tests")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || !strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "gometer_test_") {
		t.Fatal("test database must use a literal loopback address and a gometer_test_ database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.pool.Exec(ctx, `DROP SCHEMA IF EXISTS gometer CASCADE`); err != nil {
		t.Fatal("test schema reset failed")
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}
func setupMembers(t *testing.T, db *Database) (string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	id, _ := NewID()
	issuer := "https://keycloak.example/realms/gometer"
	admin, err := db.Bootstrap(ctx, id, "Fixture workspace", Identity{issuer, "admin-subject", "Administrator"})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := db.SetMember(ctx, id, admin, Identity{issuer, "operator-subject", "Operator"}, "operator", true)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := db.SetMember(ctx, id, admin, Identity{issuer, "viewer-subject", "Viewer"}, "viewer", true)
	if err != nil {
		t.Fatal(err)
	}
	return id, admin, operator, viewer
}

var definitionJSON = json.RawMessage(`{"name":"Checkout smoke","url":"http://127.0.0.1:1/private?token=PRIVATEQUERY","headers":{"Authorization":"PRIVATEHEADER"},"mode":"rate","rate":1,"duration":"1s"}`)

func TestPostgresDefinitionsPersistAndEnforceRoles(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	workspaceID, admin, operator, viewer := setupMembers(t, db)
	if _, err := db.CreateDefinition(ctx, workspaceID, viewer, definitionJSON); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer wrote: %v", err)
	}
	original, err := db.CreateDefinition(ctx, workspaceID, operator, definitionJSON)
	if err != nil {
		t.Fatal(err)
	}
	if original.Creator.ID != operator || original.Editor.ID != operator || original.Revision != 1 {
		t.Fatalf("attribution: %+v", original)
	}
	copy, err := db.CopyDefinition(ctx, workspaceID, admin, original.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if copy.ID == original.ID || copy.Creator.ID != admin || copy.Revision != 1 || copy.Source == nil || copy.Source.ID != original.ID || copy.Source.Revision != 1 {
		t.Fatalf("copy provenance: %+v", copy)
	}
	_, err = db.UpdateDefinition(ctx, workspaceID, operator, copy.ID, 1, json.RawMessage(`{"name":"Changed copy","url":"http://127.0.0.1:1"}`))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, os.Getenv("GOMETER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Check(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := reopened.ListDefinitions(ctx, workspaceID, viewer, "", false)
	if err != nil || len(list) != 2 {
		t.Fatalf("reopened list: %d %v", len(list), err)
	}
	for _, item := range list {
		if len(item.Config) != 0 {
			t.Fatal("list returned raw config")
		}
		if item.ID == original.ID && item.Name != "Checkout smoke" {
			t.Fatal("copy update mutated original")
		}
	}
	encoded, _ := json.Marshal(list)
	for _, secret := range []string{"PRIVATEQUERY", "PRIVATEHEADER"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("metadata leaked sensitive config")
		}
	}
	mine, err := reopened.ListDefinitions(ctx, workspaceID, admin, "", true)
	if err != nil || len(mine) != 1 || mine[0].ID != copy.ID {
		t.Fatal("My tests did not use creator identity")
	}
	found, err := reopened.ListDefinitions(ctx, workspaceID, viewer, "Operator", false)
	if err != nil || len(found) != 2 {
		t.Fatal("creator/editor search failed")
	}
	literal, err := reopened.ListDefinitions(ctx, workspaceID, viewer, "%", false)
	if err != nil || len(literal) != 0 {
		t.Fatal("search treated literal percent as a wildcard")
	}
	var count int
	if db.pool.QueryRow(ctx, `SELECT count(*) FROM gometer.audit_events WHERE workspace_id=$1 AND action LIKE 'test.%'`, workspaceID).Scan(&count) != nil || count != 3 {
		t.Fatalf("audit count: %d", count)
	}
}

func TestPostgresRevisionRaceAcrossIndependentConnections(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	workspaceID, admin, operator, _ := setupMembers(t, db)
	saved, err := db.CreateDefinition(ctx, workspaceID, admin, definitionJSON)
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
			_, err := connection.UpdateDefinition(ctx, workspaceID, operator, saved.ID, 1, definitionJSON)
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
		t.Fatalf("race: %d saves, %d conflicts", successes, conflicts)
	}
	if _, err := other.CopyDefinition(ctx, workspaceID, operator, saved.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale copy: %v", err)
	}
}

func TestPostgresRevocationWorkspaceIsolationAndAdminProtection(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	workspaceID, admin, operator, viewer := setupMembers(t, db)
	issuer := "https://keycloak.example/realms/gometer"
	if _, err := db.SetMember(ctx, workspaceID, operator, Identity{issuer, "viewer-subject", "Viewer"}, "admin", true); !errors.Is(err, ErrForbidden) {
		t.Fatal("operator changed membership")
	}
	if _, err := db.SetMember(ctx, workspaceID, admin, Identity{issuer, "admin-subject", "Administrator"}, "viewer", true); err == nil {
		t.Fatal("last admin removed")
	}
	if _, err := db.Bootstrap(ctx, workspaceID, "Overwrite", Identity{issuer, "attacker", "Attacker"}); err == nil {
		t.Fatal("bootstrap reopened existing workspace")
	}
	_, err := db.SetMember(ctx, workspaceID, admin, Identity{issuer, "operator-subject", "Renamed in LDAP"}, "operator", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateDefinition(ctx, workspaceID, operator, definitionJSON); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked member wrote")
	}
	if _, err := db.ResolveMember(ctx, workspaceID, issuer, "operator-subject"); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled member resolved")
	}
	enabled, err := db.SetMember(ctx, workspaceID, admin, Identity{issuer, "operator-subject", "New display name"}, "operator", true)
	if err != nil || enabled != operator {
		t.Fatal("display name changed identity")
	}
	for _, identity := range [][2]string{{"https://another.example/realms/gometer", "operator-subject"}, {issuer, "not-a-member"}} {
		if _, err := db.ResolveMember(ctx, workspaceID, identity[0], identity[1]); !errors.Is(err, ErrForbidden) {
			t.Fatal("wrong issuer/subject resolved")
		}
	}
	otherWorkspace, _ := NewID()
	if _, err := db.Bootstrap(ctx, otherWorkspace, "Other", Identity{issuer, "other-admin", "Other admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListDefinitions(ctx, otherWorkspace, viewer, "", false); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross-workspace read allowed")
	}
}

func TestPostgresMigrationConcurrencyAndHistoryValidation(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	other, err := Open(ctx, os.Getenv("GOMETER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, connection := range []*Database{db, other} {
		wg.Add(1)
		go func(connection *Database) { defer wg.Done(); errs <- connection.Migrate(ctx) }(connection)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO gometer.schema_migrations(version,checksum) VALUES(99,'future')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); !errors.Is(err, ErrSchema) {
		t.Fatal("future schema accepted")
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM gometer.schema_migrations WHERE version=99`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE gometer.schema_migrations SET checksum='changed' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); !errors.Is(err, ErrSchema) {
		t.Fatalf("tampered history accepted: %v", err)
	}
	if err := db.Check(ctx); !errors.Is(err, ErrSchema) {
		t.Fatal("readiness accepted tampered history")
	}
}

func TestDatabaseDSNErrorsNeverEchoCredentials(t *testing.T) {
	for _, dsn := range []string{"postgres://user:PRIVATEPASSWORD@%XX:5432/db", "postgres://user:PRIVATEPASSWORD@database.example/db?sslmode=disable", "postgres://user:PRIVATEPASSWORD@database.example/db?sslmode=require", "host=127.0.0.1,remote.example user=user password=PRIVATEPASSWORD dbname=db sslmode=disable"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		db, err := Open(ctx, dsn)
		cancel()
		if db != nil {
			db.Close()
			t.Fatal("unsafe DSN accepted")
		}
		if err == nil || strings.Contains(err.Error(), "PRIVATEPASSWORD") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestPostgresFailedAuditRollsBackDefinitionAcceptance(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	workspaceID, _, operator, _ := setupMembers(t, db)
	_, err := db.pool.Exec(ctx, `CREATE FUNCTION gometer.reject_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture audit storage failure';END$$; CREATE TRIGGER reject_test_audit BEFORE INSERT ON gometer.audit_events FOR EACH ROW WHEN (NEW.action='test.create') EXECUTE FUNCTION gometer.reject_test_audit()`)
	if err != nil {
		t.Fatal("could not install failure fixture")
	}
	if _, err := db.CreateDefinition(ctx, workspaceID, operator, definitionJSON); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed audit accepted a test: %v", err)
	}
	var count int
	if db.pool.QueryRow(ctx, `SELECT count(*) FROM gometer.test_definitions WHERE workspace_id=$1`, workspaceID).Scan(&count) != nil || count != 0 {
		t.Fatal("failed transaction left a saved definition")
	}
}
