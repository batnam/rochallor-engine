# Workflow Modeller

Visual editor for the Rochallor Workflow Engine

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

## Drift guard

`pnpm test:drift` runs every fixture under `tests/fixtures/` through both the TypeScript validator (`src/domain/validate.ts`) and the Go validator (`workflow-engine/cmd/validate-fixture`). Any case where the editor accepts JSON the engine would reject (or vice versa) fails the suite. This is the load-bearing guarantee for SC-002 — do not disable it.

> **`workflow-engine/validate-fixture` — compiled binary, not source**
>
> The `workflow-engine/` directory contains a pre-built binary named `validate-fixture`. It is the compiled form of `workflow-engine/cmd/validate-fixture/main.go` — a small Go CLI that runs the engine's authoritative parser and validator against a single workflow JSON file and prints `{"accepted": bool, "error": "..."}` on stdout. It is called by the workflow-modeller's drift-guard harness to verify that every fixture accepted by the TypeScript validator is also accepted by the Go engine (the mechanical guarantee that the two implementations stay in sync). You do not need to run it directly; the modeller's test suite invokes it automatically.

## Supported step types

The palette offers every step type the engine understands: `SERVICE_TASK`, `USER_TASK`, `DECISION`, `DECISION_TABLE`, `TRANSFORMATION`, `WAIT`, `PARALLEL_GATEWAY`, `JOIN_GATEWAY`, and `END`.

**Decision Table** (the orange diamond with a table glyph) authors a rule grid in one step: rows are rules, left-side columns are boolean input expressions, right-side columns are output variable assignments, and each row names a target step. The engine evaluates rules top-to-bottom and the first match wins (FIRST hit policy). Use it in place of a long chain of `DECISION` steps when the routing depends on two or more variables. Output cell values follow the same encoding as `TRANSFORMATION`: a bare JSON literal (`"GOLD"`, `0.5`, `true`) is stored as that literal; a `${expression}` string is treated as a templated expression. An empty `when` map is a catch-all rule; an empty `defaultNextStep` makes the step fail at runtime when no rule matches (intentional fail-fast behavior).
