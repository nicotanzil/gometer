# Team workspace backend foundations

[Documentation](README.md) · [CLI reference](cli.md)

Status: local implementation, 24 October 2026. PostgreSQL storage and OIDC
verification boundaries are implemented and fixture-tested. The dashboard still
uses personal JSON storage. Browser login, shared HTTP APIs, managed secret
resolution, the durable queue, executor and team Kubernetes manifests are not
connected yet. Setting a database URL does not silently switch `gometer ui` to
team mode or import existing files.

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

The diagram describes the intended browser integration. Its OIDC code-exchange
and membership components are implemented; the browser/session adapter is pending.
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
| `GOMETER_OIDC_REDIRECT_URI` | Exact future HTTPS callback URI registered with Keycloak |
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

Pending login transactions are single-use and expire after ten minutes. They are
not durable application sessions. The future HTTP adapter must bind them to the
initiating browser and implement session/CSRF/logout/revocation behavior. None of
those protections is implied merely by successful discovery.

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
in the personal dashboard. Before team HTTP integration, managed secret references,
permitted destinations and data/export scopes must be enforced at that boundary.
Existing personal records are not automatically copied into a shared database.

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
transaction rollback and migration validation. Normal unit tests skip database
cases when no explicitly named fixture URL is supplied.

OIDC tests use a local TLS provider fixture with signed tokens and counted token
requests. They cover positive exchange, claim/signature failures, PKCE/state/nonce,
expiry, redirect denial and callback races. They do not run a real Keycloak server,
LDAP service, company SSO or Kubernetes deployment. Integration with those systems
remains a separate acceptance step.
