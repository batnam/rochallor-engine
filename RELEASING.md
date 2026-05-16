# Release Process

## One-time Setup (do this before the first release)

### 1. PyPI — Trusted Publishing (no API token needed)

1. Create account at [pypi.org](https://pypi.org) if you don't have one
2. Go to **Account Settings → Publishing → Add a new pending publisher**
3. Fill in:
   - PyPI Project Name: `rochallor-sdk`
   - Owner: `batnam`
   - Repository: `rochallor-engine`
   - Workflow: `publish.yml`
   - Environment: `pypi`
4. In GitHub repo → **Settings → Environments** → create an environment named `pypi`

No API token is stored — PyPI verifies the GitHub Actions OIDC token automatically.

### 2. npm — Auth Token

1. Create account at [npmjs.com](https://www.npmjs.com) if you don't have one
2. Go to **Access Tokens → Generate New Token → Automation**
3. Copy the token
4. In GitHub repo → **Settings → Secrets → Actions** → add secret `NPM_TOKEN`

### 3. GitHub Packages (Java SDK)

No setup needed — uses the auto-provided `GITHUB_TOKEN` with `packages: write` permission.

Users who want to consume the Java SDK need a GitHub Personal Access Token with `read:packages` scope.

### 4. GHCR (Docker image)

No setup needed — uses the auto-provided `GITHUB_TOKEN` with `packages: write` permission.

After the first push, go to **GitHub → Packages → rochallor-engine → Package Settings** and set visibility to **Public**.

---

## Releasing a New Version

### Step 1 — Update CHANGELOG.md

Move items from `[Unreleased]` to a new version section:

```markdown
## [1.1.0] - 2026-06-01

### Added
- ...

### Fixed
- ...
```

### Step 2 — Commit and push

```bash
git add CHANGELOG.md
git commit -m "chore: release v1.1.0"
git push origin main
```

### Step 3 — Push the version tag

```bash
git tag v1.1.0
git push origin v1.1.0
```

This triggers the `publish.yml` workflow which:
- Builds and pushes `ghcr.io/batnam/rochallor-engine:v1.1.0` and `:latest`
- Publishes `rochallor-sdk==1.1.0` to PyPI
- Publishes `rochallor-workflow-sdk@1.1.0` to npm
- Publishes `com.batnam:workflow-sdk-java:1.1.0` to GitHub Packages
- Creates tag `workflow-sdk-go/v1.1.0` for Go module versioning
- Creates a GitHub Release with auto-generated release notes

### Step 4 — Verify

| Check | URL |
|-------|-----|
| Docker image | `ghcr.io/batnam/rochallor-engine` |
| PyPI package | `https://pypi.org/project/rochallor-sdk/` |
| npm package | `https://www.npmjs.com/package/rochallor-workflow-sdk` |
| Java package | GitHub → Packages tab |
| GitHub Release | GitHub → Releases tab |

---

## SDK Version Alignment

All SDKs are versioned together with the engine. When you release `v1.1.0`:
- Engine image → `v1.1.0`
- Python SDK → `1.1.0`
- Node SDK → `1.1.0`
- Java SDK → `1.1.0`
- Go SDK tag → `workflow-sdk-go/v1.1.0`
