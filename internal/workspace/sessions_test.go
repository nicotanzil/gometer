package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresSessionsPersistRotateAndRecheckMembership(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, operator, _ := setupMembers(t, db)
	issuer := "https://keycloak.example/realms/gometer"
	token, session, err := db.NewSession(ctx, wid, issuer, "operator-subject", time.Now().Add(time.Hour), "")
	if err != nil || session.Member.ID != operator || session.Member.Role != "operator" {
		t.Fatalf("session: %v", err)
	}
	other, err := Open(ctx, os.Getenv("GOMETER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.ResolveSession(ctx, wid, token); err != nil {
		t.Fatal("session did not survive reopening database")
	}
	if _, err = other.ResolveSession(ctx, strings.Repeat("0", 32), token); !errors.Is(err, ErrForbidden) {
		t.Fatal("session crossed workspace")
	}
	if _, _, err = db.NewSession(ctx, wid, "https://another.example", "operator-subject", time.Now().Add(time.Hour), ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("unverified issuer mapped to existing member")
	}
	rotated, _, err := other.NewSession(ctx, wid, issuer, "operator-subject", time.Now().Add(time.Hour), token)
	if err != nil {
		t.Fatal(err)
	}
	if token == rotated {
		t.Fatal("session was not rotated")
	}
	if _, err = db.ResolveSession(ctx, wid, token); !errors.Is(err, ErrForbidden) {
		t.Fatal("old session accepted after rotation")
	}
	if _, err = db.SetMember(ctx, wid, admin, Identity{issuer, "operator-subject", "Changed name"}, "viewer", true); err != nil {
		t.Fatal(err)
	}
	current, err := other.ResolveSession(ctx, wid, rotated)
	if err != nil || current.Member.Role != "viewer" || current.Member.Name != "Changed name" {
		t.Fatal("session trusted cached membership")
	}
	var hash string
	if err = db.pool.QueryRow(ctx, `SELECT token_hash FROM gometer.sessions WHERE workspace_id=$1`, wid).Scan(&hash); err != nil || hash == rotated || len(hash) != 64 {
		t.Fatal("raw session token stored")
	}
	if _, err = db.SetMember(ctx, wid, admin, Identity{issuer, "operator-subject", "Changed name"}, "viewer", false); err != nil {
		t.Fatal(err)
	}
	if _, err = other.ResolveSession(ctx, wid, rotated); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled member retained access")
	}
	if _, err = db.SetMember(ctx, wid, admin, Identity{issuer, "operator-subject", "Changed name"}, "viewer", true); err != nil {
		t.Fatal(err)
	}
	if _, err = other.ResolveSession(ctx, wid, rotated); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled session revived after reenable")
	}
	fresh, _, err := db.NewSession(ctx, wid, issuer, "operator-subject", time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if err = other.RevokeSession(ctx, wid, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ResolveSession(ctx, wid, fresh); !errors.Is(err, ErrForbidden) {
		t.Fatal("logged-out session accepted")
	}
	expiring, _, err := db.NewSession(ctx, wid, issuer, "operator-subject", time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, `UPDATE gometer.sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE workspace_id=$1`, wid); err != nil {
		t.Fatal(err)
	}
	if _, err = other.ResolveSession(ctx, wid, expiring); !errors.Is(err, ErrForbidden) {
		t.Fatal("expired session accepted")
	}
	var leak bool
	if db.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gometer.audit_events WHERE entity_id=$1 OR action=$1)`, fresh).Scan(&leak) != nil || leak {
		t.Fatal("audit stored session credential")
	}
}

func TestPostgresPendingLoginConsumedOnceAcrossConnections(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, _, _, _ := setupMembers(t, db)
	other, err := Open(ctx, os.Getenv("GOMETER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	browser, _ := NewBrowserToken()
	if err = db.SaveLogin(ctx, wid, browser, "", []byte("encrypted fixture"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConsumeLogin(ctx, strings.Repeat("0", 32), browser); !errors.Is(err, ErrForbidden) {
		t.Fatal("pending login crossed workspace")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, connection := range []*Database{db, other} {
		wg.Add(1)
		go func(connection *Database) {
			defer wg.Done()
			_, err := connection.ConsumeLogin(ctx, wid, browser)
			results <- err
		}(connection)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrForbidden) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("pending login consumed more than once")
	}
	if err = db.SaveLogin(ctx, wid, browser, "", []byte("encrypted fixture"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, `UPDATE gometer.pending_logins SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConsumeLogin(ctx, wid, browser); !errors.Is(err, ErrForbidden) {
		t.Fatal("expired login accepted")
	}
}

func TestPostgresSessionAuditFailureRollsBack(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, _, _, _ := setupMembers(t, db)
	_, err := db.pool.Exec(ctx, `CREATE FUNCTION gometer.reject_session_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture audit failure';END$$; CREATE TRIGGER reject_session_audit BEFORE INSERT ON gometer.audit_events FOR EACH ROW WHEN (NEW.action='session.login') EXECUTE FUNCTION gometer.reject_session_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	if token, _, err := db.NewSession(ctx, wid, "https://keycloak.example/realms/gometer", "operator-subject", time.Now().Add(time.Hour), ""); !errors.Is(err, ErrUnavailable) || token != "" {
		t.Fatal("session accepted without audit")
	}
	var count int
	if db.pool.QueryRow(ctx, `SELECT count(*) FROM gometer.sessions`).Scan(&count) != nil || count != 0 {
		t.Fatal("failed audit left a session")
	}
}

func TestPostgresUpgradeFromWorkspaceFoundationPreservesTests(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	wid, admin, _, _ := setupMembers(t, db)
	saved, err := db.CreateDefinition(ctx, wid, admin, definitionJSON)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the prior committed schema: only the first migration is applied.
	if _, err = db.pool.Exec(ctx, `DROP TABLE gometer.sessions; DROP TABLE gometer.pending_logins; DELETE FROM gometer.schema_migrations WHERE version=2`); err != nil {
		t.Fatal(err)
	}
	if err = db.Check(ctx); !errors.Is(err, ErrSchema) {
		t.Fatal("old schema accepted as ready")
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.Check(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListDefinitions(ctx, wid, admin, "", false)
	if err != nil || len(list) != 1 || list[0].ID != saved.ID {
		t.Fatal("session migration changed existing definitions")
	}
}
