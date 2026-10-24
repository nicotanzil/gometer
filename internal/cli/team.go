package cli

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gometer/internal/identity"
	"gometer/internal/team"
	"gometer/internal/workspace"
)

func workspaceServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("workspace serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := envString(fs, "listen", "GOMETER_TEAM_LISTEN", "127.0.0.1:8791", "team sign-in IP and port behind HTTPS ingress")
	origin := envString(fs, "public-origin", "GOMETER_TEAM_PUBLIC_ORIGIN", "", "exact HTTPS team origin")
	id := envString(fs, "id", "GOMETER_WORKSPACE_ID", "", "explicit initialized workspace ID")
	issuer := envString(fs, "issuer", "GOMETER_OIDC_ISSUER", "", "exact HTTPS Keycloak realm issuer")
	clientID := envString(fs, "client-id", "GOMETER_OIDC_CLIENT_ID", "", "confidential OIDC client ID")
	redirect := envString(fs, "redirect-uri", "GOMETER_OIDC_REDIRECT_URI", "", "exact HTTPS callback; must be public origin + /auth/callback")
	ca := envString(fs, "oidc-ca", "GOMETER_OIDC_CA", "", "optional additional PEM CA file")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "workspace serve accepts options only")
		return 2
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("GOMETER_LOGIN_KEY"))
	if err != nil || len(key) != 32 || !workspace.ValidID(*id) || *origin == "" || *redirect != *origin+team.CallbackPath {
		fmt.Fprintln(stderr, "Set GOMETER_WORKSPACE_ID, an exact HTTPS GOMETER_TEAM_PUBLIC_ORIGIN, GOMETER_OIDC_REDIRECT_URI matching that origin + /auth/callback, and GOMETER_LOGIN_KEY (base64-encoded 32 random bytes).")
		return 2
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := workspace.Open(startup, os.Getenv("GOMETER_DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer db.Close()
	if err = db.Check(startup); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err = db.CheckWorkspace(startup, *id); err != nil {
		fmt.Fprintln(stderr, "Initialize the selected workspace before starting team sign-in.")
		return 2
	}
	provider, err := identity.New(startup, identity.Config{Issuer: *issuer, ClientID: *clientID, ClientSecret: os.Getenv("GOMETER_OIDC_CLIENT_SECRET"), RedirectURI: *redirect, CAFile: *ca})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer provider.Close()
	handler, err := team.New(db, provider, team.Options{Origin: *origin, WorkspaceID: *id, LoginKey: key})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cancel()
	if err = team.Serve(ctx, *listen, handler, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}
