package cli

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestWorkspaceCommandInputAndCredentialSafeErrors(t *testing.T) {
	for _, args := range [][]string{{"workspace"}, {"workspace", "migrate", "-help"}, {"workspace", "check", "-help"}, {"workspace", "init", "-help"}} {
		var out, errOut bytes.Buffer
		if code := Run(context.Background(), args, &out, &errOut); code != 0 {
			t.Fatalf("help: %d %s", code, errOut.String())
		}
	}
	t.Setenv("GOMETER_DATABASE_URL", "")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"workspace", "check"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "GOMETER_DATABASE_URL") {
		t.Fatal("missing database setting not explained")
	}
	t.Setenv("GOMETER_DATABASE_URL", "postgres://user:PRIVATEPASSWORD@remote.example/database?sslmode=disable")
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"workspace", "check"}, &out, &errOut); code != 2 || strings.Contains(errOut.String(), "PRIVATEPASSWORD") {
		t.Fatal("unsafe DSN handling")
	}
}
func TestPostgresWorkspaceAdministrationCommands(t *testing.T) {
	dsn := os.Getenv("GOMETER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL fixture required")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || !strings.HasPrefix(u.Path, "/gometer_test_") {
		t.Fatal("refusing a non-fixture database")
	}
	t.Setenv("GOMETER_DATABASE_URL", dsn)
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("fixture connection failed")
	}
	defer connection.Close(ctx)
	if _, err = connection.Exec(ctx, `DROP SCHEMA IF EXISTS gometer CASCADE`); err != nil {
		t.Fatal("fixture reset failed")
	}
	execute := func(args ...string) (int, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := Run(ctx, append([]string{"workspace"}, args...), &out, &errOut)
		return code, out.String() + errOut.String()
	}
	if code, _ := execute("check"); code != 2 {
		t.Fatal("unmigrated schema accepted")
	}
	for range 2 {
		if code, message := execute("migrate"); code != 0 {
			t.Fatalf("migration: %s", message)
		}
	}
	if code, message := execute("check"); code != 0 || !strings.Contains(message, "schema is ready") {
		t.Fatalf("check: %s", message)
	}
	t.Setenv("GOMETER_WORKSPACE_ID", "11111111111111111111111111111111")
	t.Setenv("GOMETER_OIDC_ISSUER", "https://keycloak.example/realms/gometer")
	args := []string{"init", "-name", "Fixture workspace", "-subject", "keycloak-federated-subject", "-display-name", "Fixture administrator"}
	if code, message := execute(args...); code != 0 || !strings.Contains(message, "11111111111111111111111111111111") {
		t.Fatalf("bootstrap: %s", message)
	}
	if code, _ := execute(args...); code != 2 {
		t.Fatal("bootstrap overwrote initialized workspace")
	}
}
