# Team workspace backend foundations

[Documentation](README.md) · [CLI reference](cli.md)

Status: local implementation, 25 October 2026. PostgreSQL storage, OIDC
verification and a separate browser sign-in service are implemented. The
personal dashboard still uses JSON storage. Team Tests metadata and secret-reference
APIs and explicitly public shared-test authoring/copy/export are available;
the [shared Tests editor](team-editor.md) and author-private recoverable drafts
are available. Execution-time secret resolution, durable queue, executor and team
Kubernetes manifests are pending. See [shared test documents](shared-tests.md)
for public-field classification and the unrestricted executor destination model.
Setting a database URL does not switch `gometer ui` to team mode or import files.

## Identity flow

The selected provider is Keycloak with LDAP User Federation:

```mermaid
flowchart LR
  Browser -->|sign in| Keycloak
  Keycloak -->|federation| LDAP
  Keycloak -->|authorization code| GoMeter
  GoMeter -->|code exchange with PKCE| Keycloak
  Keycloak -->|signed ID token| GoMeter
  GoMeter -->|verified issuer and subject| Membership[(PostgreSQL membership)]
```

The separate `workspace serve` command implements this browser sign-in flow and
the authenticated team APIs.
After sign-in it opens the shared Tests editor. The run queue is not enabled.
GoMeter has no LDAP bind DN, directory URL or LDAP password setting. LDAP
federation and directory authentication stay in Keycloak. Keycloak documents its
[OIDC discovery and endpoints](https://www.keycloak.org/securing-apps/oidc-layers)
and [LDAP federation](https://www.keycloak.org/docs/latest/server_admin/index.html#_ldap).

GoMeter identifies people by the exact verified **issuer + subject**, not email,
username, display name, LDAP DN or a forwarded browser header. A display-name
change does not create a new identity. An identity from another issuer does not
inherit an existing subject's membership. A Keycloak administrator is not
implicitly a GoMeter workspace administrator.

## PostgreSQL setup commands

Use a dedicated database. Supply connection credentials through the process
manager or Kubernetes Secret as `GOMETER_DATABASE_URL`; do not commit them.
Remote connections require verified TLS and hostname verification, including all
configured failover hosts. Example shape, with placeholders:

```text
postgres://gometer:REPLACE_ME@postgres.example:5432/gometer?sslmode=verify-full&sslrootcert=/run/gometer/postgres-ca.pem
```

Literal loopback IPs and local Unix sockets also support local fixture transport.
Connection passwords are not printed by workspace commands. Migrations require a
database account permitted to create the `gometer` schema and its objects.

With `GOMETER_DATABASE_URL` set:

```sh
./bin/gometer workspace migrate
./bin/gometer workspace check
```

`migrate` takes a transaction-scoped advisory lock, verifies recorded checksums,
and commits the schema version atomically. Concurrent migration commands serialize.
Changed migration history and a future schema version are rejected. `check` reads
schema readiness without creating objects. Neither command executes a workload,
contacts workers, or modifies personal JSON workspaces.

Initialize one workspace and its first administrator explicitly:

```sh
export GOMETER_OIDC_ISSUER=https://keycloak.example/realms/team
export GOMETER_WORKSPACE_NAME='Performance team'
./bin/gometer workspace init \
  -subject 'EXACT_KEYCLOAK_SUBJECT' \
  -display-name 'Initial administrator'
```

Use the verified token's `sub` value for that realm. The placeholder above is not
a username or a real account. This database-owner command records membership; it
does not verify a login or grant access through the current dashboard.

The command prints a generated workspace ID and internal actor ID. Retain the
workspace ID as `GOMETER_WORKSPACE_ID` for subsequent configuration. Omitting the
ID on a later init creates another workspace. Reusing an initialized workspace ID
is rejected; bootstrap cannot replace its administrator or add more members.
The repository prevents removal of the last enabled administrator.

## Keycloak preparation and discovery check

In your existing Keycloak realm, configure a confidential OIDC client using
Authorization Code flow, `client_secret_basic`, PKCE S256 and RS256 ID tokens.
Register the exact HTTPS callback URI intended for GoMeter. Leave LDAP User
Federation in Keycloak; the application does not configure the directory.
Console labels and LDAP mapper behavior depend on your Keycloak version.

Configure the backend through environment settings:

| Variable | Purpose |
| --- | --- |
| `GOMETER_OIDC_ISSUER` | Exact HTTPS realm issuer, including any deployment prefix |
| `GOMETER_OIDC_CLIENT_ID` | GoMeter's OIDC client ID |
| `GOMETER_OIDC_CLIENT_SECRET` | Confidential client credential, server-side only |
| `GOMETER_OIDC_REDIRECT_URI` | Exact HTTPS callback URI registered with Keycloak |
| `GOMETER_OIDC_CA` | Optional additional PEM CA certificate file for private Keycloak TLS |

Then run:

```sh
./bin/gometer workspace oidc-check
```

The check retrieves and validates discovery metadata over verified HTTPS. It does
not exchange a code, authenticate an LDAP user, test client registration, enable
browser login, or send workload. It can run before configuring PostgreSQL.
Flags `-issuer`, `-client-id`, `-redirect-uri` and `-oidc-ca` override their environment
settings, including explicit empty values. Client secrets are environment-only.

The verification component generates random state, nonce and S256 PKCE values.
It rejects mismatched issuer/audience/authorized-party claims, invalid signatures,
expired tokens, incorrect nonce and repeated callback consumption. Token exchanges
never follow redirects. Responses are bounded; provider errors do not echo raw
credential/token responses. Public principal values contain identity and expiry,
not ID/access/refresh tokens or the client secret.

The protocol verifier alone does not create an application session. Browser
binding, durable consumption, session cookies, CSRF and local logout belong to
the separate sign-in service below. Successful discovery alone enables none of
those behaviors.

## Browser sign-in service

This is a separate staged service, not a switch for the personal dashboard.
After migrating and explicitly initializing the workspace, configure:

| Variable | Purpose |
| --- | --- |
| `GOMETER_WORKSPACE_ID` | Explicit initialized workspace ID |
| `GOMETER_TEAM_LISTEN` | Backend IP and port; default `127.0.0.1:8791` |
| `GOMETER_TEAM_PUBLIC_ORIGIN` | Exact HTTPS origin used by the browser, without a path or trailing slash |
| `GOMETER_LOGIN_KEY` | Base64-encoded 32 random bytes, shared by sign-in instances and injected as a secret |

Use the OIDC settings above and register
`GOMETER_TEAM_PUBLIC_ORIGIN` + `/auth/callback` as the exact redirect URI.
Generate the login key once with a cryptographic source, store it in your secret
manager, and inject the same value after restarts. Do not generate a different
key per instance or put it in command arguments. Key rotation invalidates pending
sign-ins; existing sessions do not depend on that key.

```sh
./bin/gometer workspace migrate
./bin/gometer workspace check
# With database, workspace, OIDC and login-key settings supplied securely:
./bin/gometer workspace serve
```

The backend serves HTTP behind a trusted HTTPS ingress. Preserve the configured
Host and expose this service only at that origin. Restrict direct access to the
backend port. Secure cookies require browser-facing HTTPS; there is no insecure
sign-in option. Forwarded identity/origin headers do not select users or URLs.
The current scratch image has no bundled system CA store. Mount trusted CA
certificates and configure `GOMETER_OIDC_CA` and PostgreSQL `sslrootcert` for
container use. Native binaries can also use their host system trust store.
Team ingress and manifests are not supplied yet.

The service offers:

| Route | Behavior |
| --- | --- |
| `GET /` | Sign-in screen or authenticated Tests editor and sign-out form |
| `POST /auth/login` | Same-origin sign-in initiation, redirected to Keycloak |
| `GET /auth/callback` | Browser-bound, single-use code exchange after Keycloak navigation |
| `GET /api/session` | Current member, expiry and session-bound CSRF token; authenticated only |
| `POST /auth/logout` | Same-origin, CSRF-protected local logout and audit |
| `GET /api/tests` and `GET /api/tests/{id}` | Current-member test metadata; All/My filtering and author search |
| `GET /api/secret-references` and `GET /api/secret-references/{id}` | Reference metadata; source filenames and values omitted |
| `POST /api/secret-references` and `PUT /api/secret-references/{id}` | Admin-only, CSRF-protected reference changes; updates require If-Match |
| `/api/drafts` and `/api/drafts/{id}` | Author-private draft metadata/read, conditional save/delete and CSRF checks |
| Shared test document routes | Public read/export for enabled members; operator/admin create/update/copy with CSRF and revision checks |

The [managed-secret and metadata API guide](managed-secrets.md) describes the
catalog schema, permissions and mounted-file reader boundary. The
[shared document guide](shared-tests.md) covers explicit public authoring, copy
and export. Personal raw APIs and execution routes are not mounted here.
Top-level navigation to the home screen is allowed from
company portals; cross-site form and session-API requests are rejected.
LDAP users are not automatically enrolled: an exact
issuer/subject must already have enabled workspace membership.

Pending sign-ins expire after ten minutes. The browser receives an opaque
256-bit cookie; its digest indexes an AES-GCM-encrypted flow in PostgreSQL. The
ciphertext binds the workspace, public origin and initiating browser. Atomic
consumption happens before code exchange, so another instance can complete the
flow but a repeated or uncertain callback cannot replay it. Failure requires a
new sign-in. Starting another sign-in in the same browser replaces its previous
pending flow. A workspace permits at most 1,000 pending flows; expired records
are removed during initiation. Each service instance permits at most 16 concurrent callback exchanges.
Ingress request-rate controls remain operational configuration.

Authenticated sessions use separate random 256-bit tokens and store only their
hashes in PostgreSQL. Cookies have the `__Host-` prefix, `Secure`, `HttpOnly`,
`Path=/`, no Domain and explicit `SameSite=Lax`. Origin checks and a separate
session-bound CSRF token protect logout; SameSite is not the only CSRF check.
Sessions expire at the earlier of ID-token expiry and eight hours. There is no
refresh-token storage or silent renewal. Current enabled membership is checked
on session reads, and disabling a member deletes their sessions. Re-enabling
membership requires a new sign-in. Login/logout acceptance and audit are atomic.

Signing out ends the local GoMeter session. Keycloak SSO logout, back-channel
logout and LDAP disable-event propagation are not implemented. A directory or
Keycloak-only account change is reflected at the next sign-in or local session
expiry; GoMeter membership revocation takes effect on the next request.

The cookie and CSRF boundaries follow the
[OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
and [CSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html).
This is not a certification or independent security audit.

## Stored definitions and authorization

The repository stores workspaces, exact issuer/subject identities, memberships,
test definitions and audit metadata. Definition writes and their audit records
commit together. A failed audit write rolls back acceptance. Audit rows contain
actor, operation, entity ID, revision and time, without workload payloads.

Each repository operation checks current enabled membership. Viewers may list
metadata; operators/admins may author definitions. Only admins may change
membership. The check and mutation have a defined transactional order, so a stale
browser role cannot bypass a committed revocation.

Definitions retain creator, last editor, timestamps, revision and optional source
ID/revision. A copy gets its own ID, creator and revision 1. Updating a copy does
not rewrite its source. My tests filters by creator; search includes creator and
last-editor display names. Metadata queries do not fetch raw configuration, and
raw configuration is excluded from automatic definition JSON encoding.

Updates compare the submitted base revision atomically. Two independent database
connections saving one revision produce one success and one conflict. The current
storage bound is 200 definitions per workspace, carried from the existing library;
this is not an approved queue or execution-capacity policy.

These are repository guarantees, not a claim that all existing dashboard routes
now have team authorization. Personal raw-configuration and download APIs remain
in the personal dashboard. Shared HTTP routes require an explicitly classified
public document and retain managed-reference revisions without resolving values.
The selected execution model allows any reachable destination; operators must be
trusted with where they send credentials. Existing personal records are not
automatically copied into a shared database.

## Verification

```sh
make check build
make test-workspace
GOTOOLCHAIN=go1.27.3 go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Use the application toolchain explicitly when launching the scanner: a tool
launched with `go run ...@version` can otherwise select an older compiler than
the application. The local source scan on 24 October 2026 reported no known
reachable vulnerabilities; scan results depend on platform and advisory updates.

The optional PostgreSQL target needs Docker and the `postgres:18` image. It creates
its own temporary, memory-backed database, binds only loopback, supplies a random
fixture password and removes its container. It never reads an application DSN.
A fixture database must use the `gometer_test_` prefix. Tests cover reconnection,
revision/copy races, membership revocation, workspace isolation, admin protection,
transaction rollback, migration upgrades, browser-session rotation and expiry,
current-role changes, disabled-account revocation and migration validation.
The runner fails if required PostgreSQL administration/startup cases are skipped.
Normal unit tests skip database
cases when no explicitly named fixture URL is supplied.

OIDC tests use a local TLS provider fixture with signed tokens and counted token
requests. They cover positive exchange, claim/signature failures, PKCE/state/nonce,
expiry, redirect denial and callback races. They do not run a real Keycloak server,
LDAP service, company SSO or Kubernetes deployment. Integration with those systems
remains a separate acceptance step.

With Playwright and Chrome configured as described in [Contributing](../CONTRIBUTING.md),
run `GOMETER_TEAM_BROWSER=1 make test-workspace` to include actual browser
navigation through the local TLS provider fixture, cookie verification, reload
and sign-out. It checks light/dark themes and four viewport widths. The browser
flow, PostgreSQL suite, environment-based service startup/shutdown and source
vulnerability scan were verified locally on 24 October 2026. Company
Keycloak/LDAP and cluster acceptance remain unverified.
