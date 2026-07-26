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

See the [Monitor operator guide](../docs/monitor.md) for container deployment,
database permissions, and security guidance.
