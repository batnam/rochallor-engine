# Release Process

This repo is a monorepo with **nine independently releasable components**, each shipped to a component-specific registry destination. To avoid forced version coupling, the CI publish workflows are split per component and triggered by **component-prefixed git tags**: pushing a tag publishes only that component's artifact.

## Tag convention

| Component | Tag pattern | Workflow file | Where it lands |
|-----------|-------------|---------------|----------------|
| Engine (Docker image) | `engine-v<x.y.z>` | `.github/workflows/publish-engine.yml` | `ghcr.io/<owner>/rochallor-engine:v<x.y.z>` (+ `:latest`) |
| Modeller (Docker image) | `modeller-v<x.y.z>` | `.github/workflows/publish-modeller.yml` | `ghcr.io/<owner>/rochallor-modeller:<x.y.z>` (+ `:latest`) |
| Monitor Frontend (Docker image) | `monitor-frontend-v<x.y.z>` | `.github/workflows/publish-monitor-frontend.yml` | `ghcr.io/<owner>/rochallor-monitor-frontend:v<x.y.z>` (+ `:latest`) |
| Monitor BFF (Docker image) | `monitor-bff-v<x.y.z>` | `.github/workflows/publish-monitor-bff.yml` | `ghcr.io/<owner>/rochallor-monitor-bff:v<x.y.z>` (+ `:latest`) |
| Python SDK | `sdk-python-v<x.y.z>` | `.github/workflows/publish-sdk-python.yml` | PyPI |
| Node SDK | `sdk-node-v<x.y.z>` | `.github/workflows/publish-sdk-node.yml` | npm |
| Java SDK | `sdk-java-v<x.y.z>` | `.github/workflows/publish-sdk-java.yml` | Maven Central |
| Go SDK | `sdk-go-v<x.y.z>` | `.github/workflows/publish-sdk-go.yml` | Creates internal tag `workflow-sdk-go/v<x.y.z>` (consumed by `go get`) |
| Helm chart | `helm-v<x.y.z>` | `.github/workflows/publish-helm.yml` | `oci://ghcr.io/<owner>/charts` |

Each tag must match the regex `<prefix>-v[0-9]+.[0-9]+.[0-9]+` — strict three-part semver, no pre-release suffixes. Tags that don't match any pattern are ignored.

The `modeller-v…` tag publishes the **web Docker image**. Desktop packages use
the separate build workflow described below.

## Desktop Modeller builds

The **Modeller Web and Desktop** workflow (`.github/workflows/modeller-desktop.yml`)
checks the web app and builds desktop packages on three operating systems.
It runs for matching changes in pull requests and pushes to `main`, and can
also be started manually with **Run workflow** in GitHub Actions.

After a successful run, open its **Artifacts** section and download the archive
for your OS. Extract the archive to find the installer:

| Artifact | Packages |
|----------|----------|
| `modeller-desktop-macos-latest` | macOS `.dmg` |
| `modeller-desktop-windows-latest` | Windows `.exe` (NSIS) and `.msi` |
| `modeller-desktop-ubuntu-22.04` | Linux `.deb` and `.AppImage` |

Choose a package that matches the target computer's CPU architecture. The
current workflow builds for each runner's architecture; it does not produce
every architecture or a universal macOS package.

The desktop version comes from `workflow-modeller/package.json`. Tauri reads it
through `src-tauri/tauri.conf.json`. For local build commands and output paths,
see [Build the modeller](development.md#build-the-modeller).

These packages are **unsigned development builds** stored as workflow artifacts.
This workflow does not create a GitHub Release or publish installers when a
`modeller-v…` tag is pushed. Signing and macOS notarization still need to be
configured for public distribution.

The packaged macOS arm64 app has been smoke-tested locally for file import/export,
clipboard, engine requests, undo/redo, and restoring drafts after restart.
Native runtime checks on Windows and Linux remain to be performed. Before
distributing a build, repeat these checks on the target OS, including editing
dialogs and cancelling file pickers.

## How to release one component

Pick the prefix from the table, append the new semver, push the tag:

```bash
# Example: release Engine 1.3.0
git tag engine-v1.3.0
git push origin engine-v1.3.0
```

The matching workflow fires automatically. Each workflow also creates a GitHub Release named `Engine v1.3.0` (etc.) with auto-generated release notes. Monitor image publication is gated by that component's verification job; the Monitor Frontend gate includes the production-shaped Playwright suite. Per-component releases are **not** marked as the repo's "latest" release — each component has its own release stream and GitHub's single "latest" pointer would be meaningless across nine streams.

## How to release multiple components together

Push several prefixed tags. Each fires its own workflow run; they execute in parallel and are independent (a failure in one does not block another):

```bash
# Example: ship engine 1.3.0 and the Go SDK 1.3.0 together
git tag engine-v1.3.0
git tag sdk-go-v1.3.0
git push origin engine-v1.3.0 sdk-go-v1.3.0
```

There is no special "release everything" tag. If a change to `proto/` requires regenerating every SDK, tag each affected SDK (`sdk-go-v…`, `sdk-node-v…`, `sdk-python-v…`, `sdk-java-v…`) plus `engine-v…` and `modeller-v…` as needed. Release Monitor Frontend and Monitor BFF independently with their respective tag prefixes; when deploying pinned versions, select a pair verified as compatible.

## Choosing a version

Each component owns its own semver chain — they are **not** synchronized across components:

- Look at the most recent tag for that component: `git tag --list 'engine-v*' --sort=-v:refname | head -1`.
- Bump by the usual semver rules (`major` for breaking, `minor` for additive, `patch` for fixes).
- The engine may sit at `engine-v1.5.0` while the Node SDK is at `sdk-node-v2.1.3` — that's expected.

For example, release the Monitor components independently:

```bash
git tag monitor-frontend-v1.0.0
git tag monitor-bff-v1.0.0
git push origin monitor-frontend-v1.0.0 monitor-bff-v1.0.0
```

The Monitor quick-start file pulls both images without registry credentials.
After each package is published for the first time, set its GHCR package
visibility to **Public** in the GitHub package settings.

## Idempotency and re-runs

Re-run a failed workflow on the same source tag in Actions. If source or artifact bytes change, use a new version. The SDK workflows have these guards:

- `publish-sdk-node` accepts an existing version only if npm integrity matches the verified tarball; different bytes fail.
- `publish-sdk-go` accepts an existing `workflow-sdk-go/v…` tag only if it points to the verified source commit.
- Python and Java do not skip existing versions. Inspect a partial publication before rerunning; never replace published bytes.

## SDK verification before publication

Each SDK tag first calls its `verify-sdk-<language>.yml` workflow. Unit/build and
external consumer checks run before all four polling/Kafka × REST/gRPC gates,
including callback, ownership and recovery faults. Failure blocks publication.
PR CI calls the same workflows for all four SDKs on SDK, engine, proto or E2E
changes. CI uses explicit Bash `pipefail` when recording verification logs.

| SDK | Candidate used by E2E and publication |
|---|---|
| Node | Version set before build; `artifacts/sdk.tgz`, SHA-256 and source metadata. Publish downloads the tested tarball. |
| Go | Exact source checkout, race/vet and external module consumer; nested module tag points at the verified commit. |
| Python | Version set before wheel/sdist build; fresh consumers verify both. E2E installs the wheel; PyPI receives those same distribution files. |
| Java | Version set before JAR/POM build; external Maven consumer and E2E resolve the candidate repository. Publish restores verified JAR/POM/module bytes and signs them without regenerating them. |

For local reproduction and package preparation, see [E2E instructions](https://github.com/batnam/rochallor-engine/blob/main/e2e/README.md).
Local planning and review evidence is kept under `docs/enhancements/`,
`testresults/` and `perf/reports/` in the review workspace. These gitignored
files are not included in the repository or this documentation site.
Checksums identify a local candidate; the tag workflow rebuilds and verifies a
candidate on the final committed source before publishing its verified bytes.

## Legacy `v<x.y.z>` tags

The repo previously used a single `v<x.y.z>` tag that triggered all seven publish jobs at once. That mechanism is **removed**. Pre-existing `v1.0.0`–`v1.1.2` tags are kept for history but will not trigger any workflow. To republish an older component version under the new scheme, tag it under the new prefix (e.g. `engine-v1.1.2`).

## Where to look when something goes wrong

- **Workflow run logs:** `Actions` tab on GitHub, filter by the relevant workflow name (`Publish Engine`, `Publish Node SDK`, etc.).
- **Required secrets:** each workflow uses different secrets; check repo Settings → Secrets:
  - GHCR (Docker images, Helm OCI): `GITHUB_TOKEN` (provided automatically).
  - PyPI: trusted publishing via OIDC (`environment: pypi`).
  - npm: trusted publishing via OIDC, configured for `publish-sdk-node.yml` in the npm package settings.
  - Maven Central: `MAVEN_CENTRAL_USERNAME`, `MAVEN_CENTRAL_PASSWORD`, `GPG_PRIVATE_KEY`, `GPG_PASSPHRASE`.
- **Re-run a single workflow** without re-tagging: in the Actions UI, open the failed run and click "Re-run jobs". The git tag stays as is.

### Engine and chart operational capabilities

The next engine minor candidate is `engine-v1.2.0` (previous tag: `engine-v1.1.2`).
It adds readiness, migration CLI and the lease/heartbeat API work tracked in
`docs/enhancements`. Until all assigned gates pass this remains a local candidate.
Engine publication depends on the engine unit/race/vet, invariant, operational,
contract and sixteen SDK combination workflow on the same source ref.
For the lease/heartbeat candidate, also review the matching G-LOAD evidence
before creating its tag. The tag workflow does not run the isolated baseline
comparisons or the two M3 60-minute soaks; a green publication verification
workflow alone does not replace those gates.

Chart `helm-v0.2.0` pins engine `v1.2.0` independently; chart publication must
follow availability of that tested engine. Its publish workflow verifies the
packaged chart against the pinned released image, then publishes the same `.tgz`
with its checksum. It no longer derives an engine tag from the Helm tag. The
local candidate tests use a separately recorded local engine image/digest and
do not count as a remote release or a hosted-CI run.
