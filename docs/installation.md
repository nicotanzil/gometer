# Install and upgrade GoMeter

[Documentation](README.md) · [First test](getting-started.md) · [Release scope](releases/v0.9.0.md)

## Choose a release package

[GoMeter v0.9.0](https://github.com/nicotanzil/gometer/releases/tag/v0.9.0)
was published on 25 October 2026 at source commit `8f68943`.

| System | Architecture | Archive |
| --- | --- | --- |
| macOS | ARM64 / Apple Silicon | `gometer_0.9.0_darwin_arm64.tar.gz` |
| Linux | AMD64 / x86-64 | `gometer_0.9.0_linux_amd64.tar.gz` |
| Windows | AMD64 / x86-64 | `gometer_0.9.0_windows_amd64.zip` |

These are the published executable targets, not a claim of full runtime testing
on every operating system. Other targets need a source build and their own
validation. The archives contain only `gometer` or `gometer.exe`; they do not
include the demo executable, example files or deployment templates. The dashboard,
guides, fonts and starter configurations are embedded in the GoMeter executable.
Go, Java and Node.js are not required to run it.

Download the matching archive and `SHA256SUMS` from that release. Check its digest
before extracting or running it. Checksums detect changed or damaged downloads;
they do not provide a separate signature or signing identity.

## macOS ARM64

In a new directory:

```sh
curl -fLO https://github.com/nicotanzil/gometer/releases/download/v0.9.0/gometer_0.9.0_darwin_arm64.tar.gz
curl -fLO https://github.com/nicotanzil/gometer/releases/download/v0.9.0/SHA256SUMS
awk '$2 == "gometer_0.9.0_darwin_arm64.tar.gz"' SHA256SUMS | shasum -a 256 -c -
```

Continue only after the check reports `OK` and exits successfully:

```sh
tar -xzf gometer_0.9.0_darwin_arm64.tar.gz
./gometer version
./gometer ui
```

## Linux AMD64

In a new directory:

```sh
curl -fLO https://github.com/nicotanzil/gometer/releases/download/v0.9.0/gometer_0.9.0_linux_amd64.tar.gz
curl -fLO https://github.com/nicotanzil/gometer/releases/download/v0.9.0/SHA256SUMS
awk '$2 == "gometer_0.9.0_linux_amd64.tar.gz"' SHA256SUMS | sha256sum -c -
```

Continue only after the check reports `OK` and exits successfully:

```sh
tar -xzf gometer_0.9.0_linux_amd64.tar.gz
./gometer version
./gometer ui
```

## Windows AMD64

In PowerShell, in a new directory:

```powershell
Invoke-WebRequest https://github.com/nicotanzil/gometer/releases/download/v0.9.0/gometer_0.9.0_windows_amd64.zip -OutFile gometer_0.9.0_windows_amd64.zip
Invoke-WebRequest https://github.com/nicotanzil/gometer/releases/download/v0.9.0/SHA256SUMS -OutFile SHA256SUMS
$row = Get-Content SHA256SUMS | Where-Object { $_ -match '  gometer_0\.9\.0_windows_amd64\.zip$' }
if (-not $row) { throw 'Archive checksum is missing' }
$expected = ($row -split '\s+')[0]
$actual = (Get-FileHash gometer_0.9.0_windows_amd64.zip -Algorithm SHA256).Hash
if ($actual -ne $expected) { throw 'Archive checksum does not match' }
Expand-Archive gometer_0.9.0_windows_amd64.zip -DestinationPath .
.\gometer.exe version
.\gometer.exe ui
```

## Start using GoMeter

The release executable should print `GoMeter 0.9.0` for `version`. The personal
dashboard prints its URL, normally `http://127.0.0.1:8790`. Opening it sends no
workload traffic. Select **Tests**, create a test for your test environment, review
its request and traffic settings, then validate it before starting a run.

For a guided exercise with a local target, follow [your first test](getting-started.md).
That walkthrough builds the demo from source. Workload execution and results
remain available in the personal dashboard and CLI.

For shared authoring, use [team workspace setup](team-workspace.md) instead.
`gometer workspace serve` needs PostgreSQL, Keycloak/OIDC configuration, membership
and browser-facing HTTPS. The team service supports Tests and private drafts;
it does not execute team runs yet. Setting a database URL does not change the
personal `gometer ui` service into a team service.

## Build from source

Install Go 1.27.3 or newer. To build the exact release, clone its tag into a new
directory:

```sh
git clone --branch v0.9.0 --depth 1 https://github.com/nicotanzil/gometer.git gometer-v0.9.0
cd gometer-v0.9.0
go build -trimpath -ldflags="-X gometer/internal/cli.Version=0.9.0" -o bin/gometer ./cmd/gometer
go build -o bin/gometer-demo ./cmd/gometer-demo
./bin/gometer version
```

On Windows, add `.exe` to both output names and invoke them through
`.\bin\gometer.exe` and `.\bin\gometer-demo.exe`. Release builds inject their
version explicitly; an ordinary development build reports `0.9.0-dev`.
Go modules download the PostgreSQL and OIDC dependencies during the build.
See [contribution checks](../CONTRIBUTING.md) before changing the source.

## Container images

The v0.9.0 container upload failed with `permission_denied: write_package`.
**Its downloads are published; its container image is pending.** Do not assume
`ghcr.io/nicotanzil/gometer:0.9.0` is available because the Git tag or release exists.

Build and push your own image from the release source if needed; the
[Kubernetes image guide](kubernetes.md#use-a-release-image-or-build-your-own)
describes the build and package access setting. Those manifests cover the
personal dashboard and worker fleet, not the team authoring service or executor.
The v0.9.0 Dockerfile bundles standard CA certificates; private company CAs still
need explicit configuration.

## Upgrade an existing installation

1. Record the old executable version and keep it available. Stop the relevant
   service before replacing its executable; do not interrupt an active workload.
2. Back up the personal data directory (`.gometer/` by default, or the path selected
   by `GOMETER_UI_DATA` / `-data`). It may contain credentials and test data; keep
   backups private. Follow the [backup procedure](operations.md#back-up-and-verify-recovery).
3. Verify and extract the new archive into a separate directory. Keep the same
   working directory or set an explicit data path when restarting the personal
   dashboard; a relative `.gometer/` path otherwise selects a different store.
4. For the team service, back up PostgreSQL and run `gometer workspace migrate`,
   then `gometer workspace check`, using the intended `GOMETER_DATABASE_URL`.
   Migration 005 adds private drafts. Do not rerun `workspace init` for an existing
   workspace. See [team setup](team-workspace.md#postgresql-setup-commands).
5. Confirm `version`, restart with the intended settings, and review saved Tests
   before running a controlled smoke test. Startup environment changes require
   a process restart. Team execution is still pending.

Database rollback is an operator restore procedure, not an automatic schema
rollback. A local fixture upgrade does not verify your production backup or restore.
