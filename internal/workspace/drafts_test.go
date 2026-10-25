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

var publicDraft = []byte(`{"schema_version":1,"public_config":{"name":"My private draft","url":"https://target.example"},"secret_bindings":[]}`)

func TestPostgresDraftsAreAuthorPrivateDurableAndRoleChecked(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, operator, viewer := setupMembers(t, db)
	if _, err := db.SaveDraft(ctx, wid, viewer, "", 0, nil, publicDraft); !errors.Is(err, ErrForbidden) {
		t.Fatal("viewer saved draft")
	}
	saved, err := db.SaveDraft(ctx, wid, operator, "", 0, nil, publicDraft)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{admin, viewer} {
		if _, err = db.Draft(ctx, wid, actor, saved.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("another actor read a private draft")
		}
		if _, err = db.SaveDraft(ctx, wid, actor, saved.ID, 1, nil, publicDraft); err == nil {
			t.Fatal("another actor updated a draft")
		}
		if err = db.DeleteDraft(ctx, wid, actor, saved.ID, 1); err == nil {
			t.Fatal("another actor removed a draft")
		}
		list, err := db.ListDrafts(ctx, wid, actor)
		if err != nil || len(list) != 0 {
			t.Fatal("draft listed for another actor")
		}
	}
	reopened, err := Open(ctx, os.Getenv("GOMETER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Draft(ctx, wid, operator, saved.ID)
	if err != nil || got.Document == nil || got.Revision != 1 {
		t.Fatal("draft not durable")
	}
	list, err := reopened.ListDrafts(ctx, wid, operator)
	if err != nil || len(list) != 1 || list[0].Document != nil {
		t.Fatal("draft list disclosed document or lost draft")
	}
	if _, err = db.SetMember(ctx, wid, admin, Identity{"https://keycloak.example/realms/gometer", "operator-subject", "Operator"}, "viewer", true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveDraft(ctx, wid, operator, saved.ID, 1, nil, publicDraft); !errors.Is(err, ErrForbidden) {
		t.Fatal("old role retained write access")
	}
	if _, err = db.Draft(ctx, wid, operator, saved.ID); err != nil {
		t.Fatal("downgraded member lost their public draft read")
	}
}
func TestPostgresDraftsRetainStaleBindingsAndBaseWithoutPublication(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, operator, _ := setupMembers(t, db)
	ref, err := db.CreateSecretReference(ctx, wid, admin, "Credential", "PRIVATE_SOURCE")
	if err != nil {
		t.Fatal(err)
	}
	test, err := db.CreateSharedDefinition(ctx, wid, operator, sharedJSON(ref))
	if err != nil {
		t.Fatal(err)
	}
	base := &DefinitionSource{ID: test.ID, Revision: 1}
	draft, err := db.SaveDraft(ctx, wid, operator, "", 0, base, sharedJSON(ref))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateSharedDefinition(ctx, wid, operator, test.ID, 1, publicDraft); err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateSecretReference(ctx, wid, admin, ref.ID, 1, ref.Name, "new-source", false); err != nil {
		t.Fatal(err)
	}
	draft, err = db.SaveDraft(ctx, wid, operator, draft.ID, 1, base, sharedJSON(ref))
	if err != nil || draft.Base.Revision != 1 {
		t.Fatal("stale work could not be retained")
	}
	raw, _ := json.Marshal(draft)
	if strings.Contains(string(raw), "PRIVATE_SOURCE") {
		t.Fatal("draft disclosed source")
	}
	if _, err = db.UpdateSharedDefinition(ctx, wid, operator, test.ID, 1, sharedJSON(ref)); err == nil {
		t.Fatal("stale/disabled draft was published")
	}
	actual, err := db.SharedDefinition(ctx, wid, operator, test.ID)
	if err != nil || actual.Revision != 2 || actual.Name != "My private draft" {
		t.Fatal("draft save mutated shared definition")
	}
	if _, err = db.SaveDraft(ctx, wid, operator, "", 0, &DefinitionSource{ID: test.ID, Revision: 99}, publicDraft); !errors.Is(err, ErrConflict) {
		t.Fatal("invented future base accepted")
	}
	if _, err = db.SaveDraft(ctx, wid, operator, "", 0, nil, definitionJSON); err == nil {
		t.Fatal("raw credentials accepted as a draft")
	}
}
func TestPostgresDraftRevisionRaceLimitAndAuditRollback(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, _, operator, _ := setupMembers(t, db)
	saved, err := db.SaveDraft(ctx, wid, operator, "", 0, nil, publicDraft)
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
			_, err := conn.SaveDraft(ctx, wid, operator, saved.ID, 1, nil, publicDraft)
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
		t.Fatal("draft was overwritten across tabs")
	}
	if err = db.DeleteDraft(ctx, wid, operator, saved.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete removed latest draft")
	}
	for i := 1; i < MaxDrafts; i++ {
		if _, err = db.SaveDraft(ctx, wid, operator, "", 0, nil, publicDraft); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.SaveDraft(ctx, wid, operator, "", 0, nil, publicDraft); !errors.Is(err, ErrDraftLimit) {
		t.Fatal("draft count unbounded")
	}
	if _, err = db.pool.Exec(ctx, `CREATE FUNCTION gometer.reject_draft_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture audit failure';END$$; CREATE TRIGGER reject_draft_audit BEFORE INSERT ON gometer.audit_events FOR EACH ROW WHEN (NEW.action='draft.delete') EXECUTE FUNCTION gometer.reject_draft_audit()`); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteDraft(ctx, wid, operator, saved.ID, 2); !errors.Is(err, ErrUnavailable) {
		t.Fatal("draft deleted without audit")
	}
	if _, err = db.Draft(ctx, wid, operator, saved.ID); err != nil {
		t.Fatal("failed audit lost draft")
	}
}
func TestPostgresDraftMigrationPreservesSharedTestsReferencesAndSessions(t *testing.T) {
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
	token, _, err := db.NewSession(ctx, wid, "https://keycloak.example/realms/gometer", "admin-subject", time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, `DROP TABLE gometer.test_drafts; DELETE FROM gometer.schema_migrations WHERE version=5`); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SharedDefinition(ctx, wid, operator, saved.ID); err != nil {
		t.Fatal("draft migration lost shared document")
	}
	if _, err = db.ResolveSession(ctx, wid, token); err != nil {
		t.Fatal("draft migration lost session")
	}
}
