# Rochallor Monitor Frontend

React frontend for the read-only Rochallor Monitor. In local development, Vite
serves the application and proxies relative `/api` requests to the Monitor BFF
at `http://localhost:3000`.

## Prerequisites

- Node.js 24.15.0
- pnpm 9.12.3
- A locally running [Monitor BFF](../workflow-monitor-bff/README.md) on port
  `3000`

## Run locally

From the repository root:

```bash
cd workflow-monitor
pnpm install --frozen-lockfile
pnpm dev
```

Open `http://localhost:5173`.

Start the BFF before using the application. The Vite development server
forwards `/api` requests to `http://localhost:3000`, so no public BFF URL is
configured in the browser.

## Operator features

- **Process Instances** supports Business Key/time/status searches and direct
  **Open by Instance ID** navigation. Definition version, current step, and
  absolute step-age cutoff filters support Overview drill-down.
- **Overview** shows current counts by definition/version and step across all
  retained instances. Search workflows by partial name or ID, ignoring case,
  across all pages; the search is preserved in the URL. The editable
  execution-age threshold starts at 30 minutes.
  Counts refresh every 15 seconds while visible; stale/error states retain their
  meaning instead of becoming zero counts.
- Instance details include current signal/task/job/timer context, a diagram,
  and table/timeline views of paginated step attempts. Snapshot comparison loads
  on demand and distinguishes returned-variable deltas from full-state records.
- **Incidents** labels historical failures and shows the latest step attempt.
- **Saved filters** supports save/apply/rename/delete independently for Instances
  and Incidents. Browser local storage holds only versioned filter preferences:
  up to 20 names per list, 80 characters per name, a 4,096-character query, and
  64 KiB per list. Absolute UTC bounds are preserved and cursors are dropped.
  Corrupt/unavailable storage does not disable ordinary searches.

Query results, variables, and snapshots stay in the in-memory TanStack Query
cache and are cleared by reload; saved filter preferences survive separately.
Diagram/ELK code remains in the lazy-loaded instance detail chunk. The initial
list and Overview do not fetch it. This is an observation tool: no workflow
mutations, authentication features, or TLS implementation are included.

## Checks

Run commands from `workflow-monitor/`:

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

The integration and browser suites require Docker:

```bash
pnpm test:integration
pnpm test:e2e
```

`test:integration` builds the BFF and runs the frontend against an isolated
PostgreSQL test container. `test:e2e` builds both Monitor images, creates and
seeds a temporary PostgreSQL database, and runs Playwright against the
production-shaped Nginx deployment.

The browser suite covers Overview drill-down, parallel execution context,
historical failures, timeline/snapshot loading, ID lookup and Back/Forward,
saved-filter persistence, and stale-data recovery after a PostgreSQL outage.
Successful feature-flow screenshots are written under `test-results/` for
visual inspection. The stack uses only isolated test data and needs no Engine
process.

See the [Monitor operator guide](../docs/monitor.md) for container deployment,
database permissions, and security guidance.
