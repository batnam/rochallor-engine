# Release Process

This repo is a monorepo with **nine independently releasable components**, each shipped to a component-specific registry destination. To avoid forced version coupling, the CI publish workflows are split per component and triggered by **component-prefixed git tags**: pushing a tag publishes only that component's artifact.

## Tag convention

| Component | Tag pattern | Workflow file | Where it lands |
|-----------|-------------|---------------|----------------|
| Engine (Docker image) | `engine-v<x.y.z>` | `.github/workflows/publish-engine.yml` | `ghcr.io/<owner>/rochallor-engine:<x.y.z>` (+ `:latest`) |
| Modeller (Docker image) | `modeller-v<x.y.z>` | `.github/workflows/publish-modeller.yml` | `ghcr.io/<owner>/rochallor-modeller:<x.y.z>` (+ `:latest`) |
| Monitor Frontend (Docker image) | `monitor-frontend-v<x.y.z>` | `.github/workflows/publish-monitor-frontend.yml` | `ghcr.io/<owner>/rochallor-monitor-frontend:v<x.y.z>` (+ `:latest`) |
| Monitor BFF (Docker image) | `monitor-bff-v<x.y.z>` | `.github/workflows/publish-monitor-bff.yml` | `ghcr.io/<owner>/rochallor-monitor-bff:v<x.y.z>` (+ `:latest`) |
| Python SDK | `sdk-python-v<x.y.z>` | `.github/workflows/publish-sdk-python.yml` | PyPI |
| Node SDK | `sdk-node-v<x.y.z>` | `.github/workflows/publish-sdk-node.yml` | npm |
| Java SDK | `sdk-java-v<x.y.z>` | `.github/workflows/publish-sdk-java.yml` | Maven Central |
| Go SDK | `sdk-go-v<x.y.z>` | `.github/workflows/publish-sdk-go.yml` | Creates internal tag `workflow-sdk-go/v<x.y.z>` (consumed by `go get`) |
| Helm chart | `helm-v<x.y.z>` | `.github/workflows/publish-helm.yml` | `oci://ghcr.io/<owner>/charts` |

Each tag must match the regex `<prefix>-v[0-9]+.[0-9]+.[0-9]+` — strict three-part semver, no pre-release suffixes. Tags that don't match any pattern are ignored.

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

If a workflow fails partway, push the same tag again (delete and recreate locally, then `git push --force origin <tag>`) **only if no artifact reached the registry yet**. Once an artifact is published, do not reuse that version — bump and tag a new one. The individual workflows have these guards built in:

- `publish-sdk-node` skips if the version already exists on npm.
- `publish-sdk-go` skips if the `workflow-sdk-go/v…` Go-module tag already exists.
- Docker / Helm / PyPI / Maven Central reject overwrites at the registry side.

## Legacy `v<x.y.z>` tags

The repo previously used a single `v<x.y.z>` tag that triggered all seven publish jobs at once. That mechanism is **removed**. Pre-existing `v1.0.0`–`v1.1.2` tags are kept for history but will not trigger any workflow. To republish an older component version under the new scheme, tag it under the new prefix (e.g. `engine-v1.1.2`).

## Where to look when something goes wrong

- **Workflow run logs:** `Actions` tab on GitHub, filter by the relevant workflow name (`Publish Engine`, `Publish Node SDK`, etc.).
- **Required secrets:** each workflow uses different secrets; check repo Settings → Secrets:
  - GHCR (Docker images, Helm OCI): `GITHUB_TOKEN` (provided automatically).
  - PyPI: trusted publishing via OIDC (`environment: pypi`).
  - npm: `NPM_TOKEN`.
  - Maven Central: `MAVEN_CENTRAL_USERNAME`, `MAVEN_CENTRAL_PASSWORD`, `GPG_PRIVATE_KEY`, `GPG_PASSPHRASE`.
- **Re-run a single workflow** without re-tagging: in the Actions UI, open the failed run and click "Re-run jobs". The git tag stays as is.
