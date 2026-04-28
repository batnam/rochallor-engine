# Workflow Modeller

Visual editor for the Rochallor Workflow Engine — feature `001-workflow-modeller`.

It reads and writes the same JSON contract the engine consumes, so you can author, validate, and publish workflow definitions without hand-editing files. The editor talks to the engine over its existing REST API; no engine-side changes are required.

## Get running in 2 minutes

```bash
cd workflow-modeller
pnpm install
pnpm dev
```

Open the URL Vite prints (default `http://localhost:5173`).

You should see an empty canvas with a palette on the left, a property panel on the right, and a toolbar across the top.

To connect to a running engine: open **Settings**, point it at your engine base URL (default `http://localhost:8080`), click **Test connection**, **Save**, then **Load from engine**. Without an engine the editor is fully usable for offline authoring.

## Scripts

| Command | Purpose |
|---|---|
| `pnpm dev` | Vite dev server with HMR. |
| `pnpm build` | Type-check then production build. |
| `pnpm preview` | Serve `dist/` on `:4173` (used by Playwright). |
| `pnpm lint` / `pnpm lint:fix` | Biome lint + format check. |
| `pnpm typecheck` | `tsc --noEmit`. |
| `pnpm test` | Vitest unit + drift suites. |
| `pnpm test:e2e` | Playwright e2e (requires `pnpm playwright install` once). |
| `pnpm test:drift` | Drift guard only — runs every fixture through TS + Go validators (`go` must be on `PATH`). |
| `pnpm size` | Build, then fail if the gzipped JS bundle exceeds 500 KB. |

## Spec docs

The behaviour and rationale are specified under `../specs/001-workflow-modeller/`:

- [`spec.md`](../specs/001-workflow-modeller/spec.md) — user stories, acceptance criteria.
- [`plan.md`](../specs/001-workflow-modeller/plan.md) — tech stack, architecture.
- [`data-model.md`](../specs/001-workflow-modeller/data-model.md) — domain entities, validation rules.
- [`contracts/workflow-json.md`](../specs/001-workflow-modeller/contracts/workflow-json.md) — JSON schema the editor reads/writes.
- [`contracts/engine-rest.md`](../specs/001-workflow-modeller/contracts/engine-rest.md) — engine REST endpoints consumed.
- [`quickstart.md`](../specs/001-workflow-modeller/quickstart.md) — five-journey walkthrough.

## Drift guard

`pnpm test:drift` runs every fixture under `tests/fixtures/` through both the TypeScript validator (`src/domain/validate.ts`) and the Go validator (`workflow-engine/cmd/validate-fixture`). Any case where the editor accepts JSON the engine would reject (or vice versa) fails the suite. This is the load-bearing guarantee for SC-002 — do not disable it.
