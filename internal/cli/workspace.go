package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"gometer/internal/identity"
	"gometer/internal/workspace"
)

func workspaceCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "usage: gometer workspace migrate|check|init|oidc-check|serve [options]")
		return 0
	}
	action := args[0]
	if action != "migrate" && action != "check" && action != "init" && action != "oidc-check" && action != "serve" {
		fmt.Fprintln(stderr, "usage: gometer workspace migrate|check|init|oidc-check|serve [options]")
		return 2
	}
	if action == "serve" {
		return workspaceServe(ctx, args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("workspace "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var id, name, issuer, subject, display *string
	var clientID, redirectURI, ca *string
	if action == "oidc-check" {
		issuer = envString(fs, "issuer", "GOMETER_OIDC_ISSUER", "", "exact HTTPS Keycloak realm issuer")
		clientID = envString(fs, "client-id", "GOMETER_OIDC_CLIENT_ID", "", "Keycloak OIDC client ID")
		redirectURI = envString(fs, "redirect-uri", "GOMETER_OIDC_REDIRECT_URI", "", "exact HTTPS callback URI")
		ca = envString(fs, "oidc-ca", "GOMETER_OIDC_CA", "", "optional PEM CA certificate for Keycloak")
	}
	if action == "init" {
		id = envString(fs, "id", "GOMETER_WORKSPACE_ID", "", "workspace ID; generated when omitted")
		name = envString(fs, "name", "GOMETER_WORKSPACE_NAME", "Team workspace", "workspace display name")
		issuer = envString(fs, "issuer", "GOMETER_OIDC_ISSUER", "", "exact HTTPS Keycloak realm issuer")
		subject = fs.String("subject", "", "exact OIDC subject for the initial administrator; not an LDAP username")
		display = fs.String("display-name", "", "administrator display name")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected workspace arguments")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if action == "oidc-check" {
		if *issuer == "" || *clientID == "" || *redirectURI == "" || os.Getenv("GOMETER_OIDC_CLIENT_SECRET") == "" {
			fmt.Fprintln(stderr, "Set GOMETER_OIDC_ISSUER, GOMETER_OIDC_CLIENT_ID, GOMETER_OIDC_CLIENT_SECRET and GOMETER_OIDC_REDIRECT_URI (or their non-secret flags).")
			return 2
		}
		provider, err := identity.New(bounded, identity.Config{Issuer: *issuer, ClientID: *clientID, ClientSecret: os.Getenv("GOMETER_OIDC_CLIENT_SECRET"), RedirectURI: *redirectURI, CAFile: *ca})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		defer provider.Close()
		fmt.Fprintln(stdout, "OIDC discovery and TLS checks passed. Client registration, LDAP federation and browser sign-in still require integration verification.")
		return 0
	}
	database, err := workspace.Open(bounded, os.Getenv("GOMETER_DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer database.Close()
	if action == "migrate" {
		if err := database.Migrate(bounded); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintln(stdout, "Workspace database migrated successfully. Existing personal JSON workspaces were not imported or modified.")
		return 0
	}
	if err := database.Check(bounded); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if action == "check" {
		fmt.Fprintln(stdout, "Workspace database schema is ready. Team dashboard sign-in and execution are not enabled by this check.")
		return 0
	}
	if *id == "" {
		*id, err = workspace.NewID()
		if err != nil {
			fmt.Fprintln(stderr, "Could not create a workspace ID.")
			return 2
		}
	}
	actorID, err := database.Bootstrap(bounded, *id, *name, workspace.Identity{Issuer: *issuer, Subject: *subject, DisplayName: *display})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "Workspace initialized: %s\nInitial administrator: %s\nIdentity must still be verified by OIDC at sign-in; this command does not enable team HTTP access.\n", *id, actorID)
	return 0
}
