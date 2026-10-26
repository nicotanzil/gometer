# Contributing to GoMeter

[Project overview](README.md) · [Documentation](docs/README.md)

Keep changes focused, measurements explicit, and each development milestone
usable. The [roadmap](ROADMAP.md) and [JMeter parity program](docs/jmeter-parity.md)
record product scope and acceptance gates.

## Development setup

Use Go 1.27.3 or newer and Node.js 18 or newer for development checks.
Go modules supply the PostgreSQL and OIDC dependencies. No frontend package
installation step is needed. The dashboard is embedded from `internal/dashboard/assets/`.
Rebuild and restart the local dashboard after frontend changes, then reload the
browser. A browser refresh alone does not replace assets in an existing binary.

```sh
make check build
```

Without Make:

```sh
go vet ./...
go test -race ./...
node --check internal/dashboard/assets/app.js
node --check internal/dashboard/assets/journey-editor.js
node --test internal/dashboard/test/*.test.cjs
go build -o bin/gometer ./cmd/gometer
go build -o bin/gometer-demo ./cmd/gometer-demo
```

Race-enabled tests require a Go-supported race-detector platform and its host C
toolchain. Tests start local fixture servers; loopback network access is needed.
The production binary has no Java or Node.js runtime requirement.

`make test-ui` runs dependency-free configuration/model tests and JavaScript
syntax checks. The optional `make test-browser` uses Playwright and its Chromium
browser to exercise the canvas against a disposable dashboard and a counted
local HTTP target. Install Playwright in your development environment (outside
the production assets), then set `PLAYWRIGHT_MODULE` to its absolute module path
if it is not on Node's module search path. `CHROME_PATH` can select an existing
Chrome/Chromium executable. This test creates and removes its own temporary data
directory; it does not use `.gometer/`. It checks editing, unfinished drafts,
dependency reordering, save/reload, literal user content, six viewport widths,
and successful/failed journeys with independently counted HTTP requests. It also
checks collapsed navigation, focus and tooltips, theme changes, mobile drawers,
History comparison, Guide/Coverage, JMX review and the CSV dialog.

The worker catalog has a separate optional browser verifier:
`node internal/dashboard/test/workers-browser.cjs`. It uses two disposable local
worker processes and a counted target, checks persistence and immutable run
identity, and exercises the Workers view in three themes at four viewport widths.
Use `GOMETER_BINARY` to select an isolated build, plus the same Playwright/Chrome
settings described above. It never opens the user's saved workspace.

The optional `node internal/dashboard/test/definitions-browser.cjs` checks
real two-session revision conflicts, copy provenance, preserved drafts, Tests
search/detail/edit navigation and responsive themes without executing workload.
Use the same Playwright and Chrome settings as the other browser verifiers.

`make test-workspace` runs PostgreSQL integration checks against a disposable
Docker `postgres:18` fixture and removes it afterward. It never uses an application
database URL. The default checks skip PostgreSQL integration cases without an
explicit test URL. OIDC tests use a private TLS provider fixture; they do not
verify a deployed Keycloak or LDAP server. Set `GOMETER_TEAM_BROWSER=1` alongside
`PLAYWRIGHT_MODULE` and `CHROME_PATH` to include the optional TLS sign-in/sign-out
browser flow, shared Tests forms/canvas, actor-private draft recovery, concurrent
test/draft conflicts, viewer controls and responsive themes. No workload data is
kept in the browser profile. Release builds require the PostgreSQL fixture checks before
packaging or publication. See [team workspace setup](docs/team-workspace.md)
and the [team release gates](docs/plans/team-workspace.md#team-release-gates--25-october-2026).

## Keep development state separate

Use a dedicated workspace when testing UI behavior:

```sh
./bin/gometer-demo
# In another terminal:
./bin/gometer ui -listen 127.0.0.1:8792 -data work/development
```

Do not run multiple dashboard processes against the same directory. `bin/`,
`results/`, `work/`, and the default `.gometer/` directory are ignored. A custom
workspace elsewhere is not automatically ignored; avoid staging it or secrets.

## Before requesting review

- Explain the concrete behavior change and its limits. Connect it to a roadmap
  item when applicable.
- Run `gofmt` on changed Go files. Run `go vet ./...`, race-enabled tests, and
  builds for changes to executable behavior.
- Add regression coverage for measurement, cancellation, persistence, parsing,
  TLS, or concurrency behavior that changed. Use a local deterministic fixture;
  do not make tests depend on an external service.
- Check affected example files with `gometer validate -config ...`.
- For dashboard changes, verify the complete affected journey in a real browser,
  check narrow layouts, and inspect warning/error logs. Preserve editor drafts
  and use isolated test history.
- Update user-facing docs and screenshots when behavior or labels change.
  Documentation-only changes need link, example, flag/default, and visual checks;
  avoid tests that only mirror prose or markup.
- Run `git diff --check`, inspect the diff, and keep generated binaries, scratch
  reports, credentials, and unrelated edits out of the change.

## Measurement invariants

Preserve the distinction between scheduled demand, admitted journeys, started
HTTP attempts, completed attempts, failed work, skipped steps, and missed demand.
Use monotonic elapsed time for scheduling and observation bounds. Do not invent
samples for work that did not run, average worker percentiles, or replace missing
telemetry with zero. See [measurement definitions](docs/measurements.md).

A performance claim needs repeatable evidence with comparable workloads, target
behavior, connections, TLS, hardware, and instrumentation. Local demo results
verify behavior; they are not performance benchmarks or proof of JMeter parity.

## Documentation and screenshots

Use relative Markdown links within the repository. Put user guides and reference
material under `docs/`, and versioned screenshots under `docs/images/`. Capture
real local-demo states with no private hosts, secrets, or personal data. Follow
[the capture notes](docs/images/README.md) when refreshing them.

The docs should describe implemented behavior. Mark partial/planned capabilities
explicitly and avoid unsupported production, compatibility, or security claims.

## Security and licensing

Use [SECURITY.md](SECURITY.md) for concerns that should not be disclosed publicly.
No license file is present in the current checkout. A maintainer license decision
is required before redistribution or a public contribution policy is finalized;
this guide does not assign a license or introduce a contributor agreement.
