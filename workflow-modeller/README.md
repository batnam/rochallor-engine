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

To connect to a running engine: open **Settings**, point it at your engine base URL (default `http://localhost:8080`), click **Test connection**, **Save**, then **Load Workflow from engine**. Without an engine the editor is fully usable for offline authoring.

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

## Desktop (Windows, macOS and Linux)

The desktop application uses Tauri 2 and the same React editor, workflow model,
validator, layout engine and JSON format as the web application. The workflow
engine still runs separately; offline authoring does not require it.

Install Node.js 24+, pnpm 9.12.3, Rust stable and the
[Tauri prerequisites for your OS](https://v2.tauri.app/start/prerequisites/).
On macOS, the Xcode Command Line Tools are required. On Windows, install the
MSVC C++ build tools and WebView2. Linux requires WebKitGTK 4.1 development libraries.

```bash
cd workflow-modeller
pnpm install --frozen-lockfile
pnpm desktop:dev
```

Build an installer for the current OS with `pnpm desktop:build`. Outputs are in
`src-tauri/target/release/bundle/`. Users of the packaged app do not need Node.js
or Rust. Desktop development requires port 5173 to be free; stop the web dev
server before starting it.

| Command | Purpose |
|---|---|
| `pnpm dev` / `pnpm build` | Web development / production assets in `dist/`. |
| `pnpm desktop:dev` | Desktop window with Vite HMR. |
| `pnpm desktop:build` | Native application and installers for the current OS. |
| `pnpm dev:desktop` / `pnpm build:desktop` | Frontend hooks used by Tauri; desktop assets go to `dist-desktop/`. |

The `Modeller Web and Desktop` GitHub Actions workflow checks the web application
and builds installers on Windows, macOS and Linux. It can also be started with
`workflow_dispatch`; installers are retained as workflow artifacts, not published
as a release. These builds are unsigned. Public distribution needs platform signing
credentials and macOS notarization. The existing Docker web release is unchanged.

### Shared code and platform adapters

`src/platform/contracts.ts` defines file selection/export, clipboard and HTTP
operations. Vite resolves `@platform` to `web.ts` by default or `desktop.ts` in
desktop mode. Both adapters are type-checked. Tauri imports stay in the desktop
adapter and are excluded from the web bundle. The engine client selects the
platform transport centrally, including requests from the store and engine browser.

Web export starts a browser download. Desktop export opens a native Save As dialog
and writes the JSON only to the selected file; cancellation leaves the workflow
untouched. Desktop file permissions are granted through the file picker, not a
blanket filesystem scope. HTTP(S) access supports arbitrary engine addresses and ports entered
in Settings, including localhost and private networks; permissions are limited to
the local application window. No remote page is granted desktop capabilities.

Drafts and the current editor state use local storage in each application's profile.
Browser and desktop data are separate and do not automatically synchronize. Existing
web storage keys are preserved. Use JSON import/export with **Include canvas layout**
to transfer workflows. Desktop Save JSON is Save As, not automatic saving to an
original path. The existing authentication setting is persisted locally as before.

Dialogs for editing names/expressions and confirming actions are shared React UI.
Undo uses Ctrl/Cmd+Z and redo uses Ctrl/Cmd+Shift+Z when focus is outside a text field
or dialog.
`dragDropEnabled: false` in Tauri keeps HTML5 palette drag-and-drop working on Windows.
Icons are generated from the existing `public/favicon.svg`.

### Desktop verification

The packaged macOS arm64 build has been checked locally for native JSON import/export,
clipboard copy, HTTP connection/browse/load/upload to a localhost mock on a custom port,
undo/redo, and restoring a draft after restarting the app. Windows and Linux native
runtime checks remain to be performed on those operating systems.

`pnpm test` covers the shared logic, platform transport, file-picker cancellation,
I/O failures and desktop adapter behavior with mocked native APIs. `pnpm test:e2e`
checks the browser application in Chromium, Firefox and WebKit; it is not a native
desktop test. Before distributing a desktop build, verify these in the packaged app
on every target OS:

- Import JSON, drag nodes, edit a decision table, undo/redo and validate.
- Save JSON including layout, reopen it, and cancel both file dialogs.
- Copy exported JSON and paste it into a text editor.
- Connect to an engine, browse/load a definition and upload a new version.
- Save a draft, quit the app, reopen it and restore the draft and canvas layout.

## Drift guard

`pnpm test:drift` runs every fixture under `tests/fixtures/` through both the TypeScript validator (`src/domain/validate.ts`) and the Go validator (`workflow-engine/cmd/validate-fixture`). Any case where the editor accepts JSON the engine would reject (or vice versa) fails the suite. This is the load-bearing guarantee for SC-002 — do not disable it.

> **`workflow-engine/validate-fixture` — compiled binary, not source**
>
> The `workflow-engine/` directory contains a pre-built binary named `validate-fixture`. It is the compiled form of `workflow-engine/cmd/validate-fixture/main.go` — a small Go CLI that runs the engine's authoritative parser and validator against a single workflow JSON file and prints `{"accepted": bool, "error": "..."}` on stdout. It is called by the workflow-modeller's drift-guard harness to verify that every fixture accepted by the TypeScript validator is also accepted by the Go engine (the mechanical guarantee that the two implementations stay in sync). You do not need to run it directly; the modeller's test suite invokes it automatically.

## Supported step types

The palette offers every step type the engine understands: `SERVICE_TASK`, `USER_TASK`, `DECISION`, `DECISION_TABLE`, `TRANSFORMATION`, `WAIT`, `PARALLEL_GATEWAY`, `JOIN_GATEWAY`, and `END`.

**Decision Table** (the orange diamond with a table glyph) authors a rule grid in one step: rows are rules, left-side columns are boolean input expressions, right-side columns are output variable assignments, and each row names a target step. The engine evaluates rules top-to-bottom and the first match wins (FIRST hit policy). Use it in place of a long chain of `DECISION` steps when the routing depends on two or more variables. Output cell values follow the same encoding as `TRANSFORMATION`: a bare JSON literal (`"GOLD"`, `0.5`, `true`) is stored as that literal; a `${expression}` string is treated as a templated expression. An empty `when` map is a catch-all rule; an empty `defaultNextStep` makes the step fail at runtime when no rule matches (intentional fail-fast behavior).
