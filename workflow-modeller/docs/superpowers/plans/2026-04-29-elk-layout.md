# ELK Layout Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace dagre with ELK (Eclipse Layout Kernel) for a port-aware, crossing-minimised workflow layout, and switch all edges to orthogonal SmoothStep routing.

**Architecture:** `layoutWithElk(nodes, edges)` is an async function in `canvas/layout.ts` that builds an ELK graph with per-node ports (matching ReactFlow Handle IDs), awaits `elk.layout()`, and returns top-left positions. Canvas uses `useEffect + useState` to run it when any node lacks a stored position; Toolbar awaits it before overwriting the store layout. `EdgeShell` switches from `getBezierPath` to `getSmoothStepPath`.

**Tech Stack:** `elkjs` (ELK algorithm compiled to JS, no worker required when importing `elkjs/lib/elk.bundled.js`), React 19, Zustand, `@xyflow/react` 12, Vitest + jsdom.

**Working directory for all commands:** `workflow-modeller/`

**Package manager:** `pnpm`. If not in `$PATH`, find it at `~/.nvm/versions/node/v24.15.0/lib/node_modules/corepack/shims/pnpm` or run `corepack enable pnpm`.

---

## File Map

| File | Action | What changes |
|---|---|---|
| `package.json` | Modify | Add `elkjs`; remove `dagre`, `@types/dagre` |
| `src/domain/types.ts` | Modify | Add `sourceHandle?: string` to `GraphEdge` |
| `src/domain/graph.ts` | Modify | Set `sourceHandle` for DECISION + PARALLEL_GATEWAY edges |
| `src/canvas/layout.ts` | Rewrite | `layoutWithElk(nodes, edges): Promise<Positions>`; delete dagre code |
| `src/canvas/edges/EdgeShell.tsx` | Modify | `getBezierPath` → `getSmoothStepPath` |
| `src/canvas/Canvas.tsx` | Modify | Async layout via `useEffect + useState`; pass `sourceHandle` to ReactFlow edges |
| `src/panels/Toolbar.tsx` | Modify | `handleTidy` becomes `async`; awaits `layoutWithElk` |
| `tests/unit/graph.test.ts` | Modify | Add `sourceHandle` assertions for conditional + parallel edges |
| `tests/unit/layout.test.ts` | Create | Unit tests for `layoutWithElk` |

---

## Task 1: Swap packages

**Files:**
- Modify: `package.json`

- [ ] **Step 1.1: Install elkjs and remove dagre**

```bash
pnpm add elkjs && pnpm remove dagre @types/dagre
```

Expected: `package.json` now shows `"elkjs"` in `dependencies` and `dagre` / `@types/dagre` are gone from both `dependencies` and `devDependencies`. A `pnpm-lock.yaml` update is normal.

- [ ] **Step 1.2: Verify installation**

```bash
node -e "import('elkjs/lib/elk.bundled.js').then(m => console.log('ELK ok:', typeof m.default))"
```

Expected output: `ELK ok: function`

- [ ] **Step 1.3: Commit**

```bash
git add package.json pnpm-lock.yaml
git commit -m "chore: replace dagre with elkjs"
```

---

## Task 2: Extend GraphEdge with sourceHandle

**Files:**
- Modify: `src/domain/types.ts:98-103`
- Modify: `src/domain/graph.ts:33-71`
- Modify: `tests/unit/graph.test.ts`

- [ ] **Step 2.1: Write failing tests**

Add to `tests/unit/graph.test.ts` after the existing `describe('toEdges', ...)` block:

```typescript
describe('toEdges sourceHandle', () => {
  it('conditional edges carry sourceHandle matching branch:<expression>', () => {
    const def = loadFixtures().find((f) => f.name === 'loan-application-full.json')?.def;
    expect(def).toBeDefined();
    if (!def) return;
    const edges = toEdges(def);
    const conditional = edges.filter((e) => e.variant.kind === 'conditional');
    expect(conditional.length).toBeGreaterThan(0);
    for (const e of conditional) {
      expect(e.sourceHandle, `edge ${e.id}`).toBeDefined();
      if (e.variant.kind === 'conditional') {
        expect(e.sourceHandle).toBe(`branch:${e.variant.expression}`);
      }
    }
  });

  it('parallel edges carry sourceHandle matching parallel:<index>', () => {
    const def = loadFixtures().find((f) => f.name === 'loan-application-full.json')?.def;
    expect(def).toBeDefined();
    if (!def) return;
    const edges = toEdges(def);
    const parallel = edges.filter((e) => e.variant.kind === 'parallel');
    expect(parallel).toHaveLength(2); // parallel-risk-checks has 2 branches
    const handles = parallel.map((e) => e.sourceHandle).sort();
    expect(handles).toEqual(['parallel:0', 'parallel:1']);
  });

  it('sequential edges do not carry sourceHandle', () => {
    const def = loadFixtures().find((f) => f.name === 'loan-application-full.json')?.def;
    expect(def).toBeDefined();
    if (!def) return;
    const edges = toEdges(def);
    for (const e of edges.filter((e) => e.variant.kind === 'sequential')) {
      expect(e.sourceHandle, `edge ${e.id}`).toBeUndefined();
    }
  });
});
```

- [ ] **Step 2.2: Run tests — expect failures**

```bash
pnpm test -- --reporter=verbose 2>&1 | grep -E "FAIL|PASS|sourceHandle"
```

Expected: tests in `toEdges sourceHandle` block fail with `sourceHandle` being `undefined`.

- [ ] **Step 2.3: Add sourceHandle to GraphEdge type**

In `src/domain/types.ts`, update the `GraphEdge` interface:

```typescript
export interface GraphEdge {
  id: string;
  from: StepId;
  to: StepId;
  variant: EdgeVariant;
  sourceHandle?: string;
}
```

- [ ] **Step 2.4: Set sourceHandle in graph.ts**

Replace the `DECISION` and `PARALLEL_GATEWAY` cases in `pushStepEdges` in `src/domain/graph.ts`:

```typescript
    case 'DECISION':
      for (const [expression, target] of Object.entries(step.conditionalNextSteps)) {
        out.push({
          ...makeEdge(step.id, target, { kind: 'conditional', expression }),
          sourceHandle: `branch:${expression}`,
        });
      }
      break;

    // ...

    case 'PARALLEL_GATEWAY':
      step.parallelNextSteps.forEach((target, i) => {
        out.push({
          ...makeEdge(step.id, target, { kind: 'parallel' }),
          sourceHandle: `parallel:${i}`,
        });
      });
      break;
```

- [ ] **Step 2.5: Run tests — expect all passing**

```bash
pnpm test -- --reporter=verbose 2>&1 | tail -20
```

Expected: all tests pass, including the three new `toEdges sourceHandle` tests.

- [ ] **Step 2.6: Commit**

```bash
git add src/domain/types.ts src/domain/graph.ts tests/unit/graph.test.ts
git commit -m "feat: add sourceHandle to GraphEdge for DECISION and PARALLEL branches"
```

---

## Task 3: Rewrite layout.ts with ELK

**Files:**
- Rewrite: `src/canvas/layout.ts`
- Create: `tests/unit/layout.test.ts`

- [ ] **Step 3.1: Write failing layout tests**

Create `tests/unit/layout.test.ts`:

```typescript
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { toEdges, toNodes } from '@/domain/graph';
import { zWorkflowDefinition } from '@/domain/schema';
import { layoutWithElk } from '@/canvas/layout';
import { describe, expect, it } from 'vitest';

const VALID_DIR = resolve(__dirname, '../fixtures/valid');

function load(name: string) {
  const raw = JSON.parse(readFileSync(join(VALID_DIR, name), 'utf8'));
  return zWorkflowDefinition.parse(raw);
}

describe('layoutWithElk', () => {
  it('returns empty object for empty node list', async () => {
    const result = await layoutWithElk([], []);
    expect(result).toEqual({});
  });

  it('positions every node in loan-application-full', async () => {
    const def = load('loan-application-full.json');
    const nodes = toNodes(def);
    const edges = toEdges(def);
    const result = await layoutWithElk(nodes, edges);
    expect(Object.keys(result)).toHaveLength(nodes.length);
    for (const node of nodes) {
      expect(result[node.id], `node ${node.id}`).toMatchObject({
        x: expect.any(Number),
        y: expect.any(Number),
      });
    }
  });

  it('entry node is leftmost (LR direction)', async () => {
    const def = load('loan-application-full.json');
    const nodes = toNodes(def);
    const edges = toEdges(def);
    const result = await layoutWithElk(nodes, edges);
    const entryX = result['validate-application']?.x ?? Number.POSITIVE_INFINITY;
    for (const [id, pos] of Object.entries(result)) {
      if (id !== 'validate-application') {
        expect(pos.x, `${id} should be right of entry`).toBeGreaterThanOrEqual(entryX);
      }
    }
  });

  it('decision branch targets land at distinct y positions', async () => {
    const def = load('loan-application-full.json');
    const nodes = toNodes(def);
    const edges = toEdges(def);
    const result = await layoutWithElk(nodes, edges);
    // route-application → end-rejected, manual-review-task, auto-approve (3 branches)
    const ys = ['end-rejected', 'manual-review-task', 'auto-approve'].map(
      (id) => result[id]?.y ?? 0,
    );
    const unique = new Set(ys.map((y) => Math.round(y / 5) * 5));
    expect(unique.size).toBeGreaterThan(1);
  });

  it('positions all nodes in loan-disbursement-workflow', async () => {
    const def = load('loan-disbursement-workflow.json');
    const nodes = toNodes(def);
    const edges = toEdges(def);
    const result = await layoutWithElk(nodes, edges);
    expect(Object.keys(result)).toHaveLength(nodes.length);
  });
});
```

- [ ] **Step 3.2: Run tests — expect failures**

```bash
pnpm test tests/unit/layout.test.ts -- --reporter=verbose 2>&1 | tail -20
```

Expected: all 5 tests fail because `layoutWithElk` does not exist yet.

- [ ] **Step 3.3: Rewrite layout.ts**

Replace the entire contents of `src/canvas/layout.ts` with:

```typescript
import type { ElkExtendedEdge, ElkNode, ElkPort } from 'elkjs';
import ELK from 'elkjs/lib/elk.bundled.js';
import type { GraphEdge, GraphNode, StepId } from '@/domain/types';

const elk = new ELK();

const NODE_WIDTH = 180;

const ELK_OPTIONS: Record<string, string> = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.spacing.nodeNode': '80',
  'elk.layered.spacing.nodeNodeBetweenLayers': '140',
  'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
  'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
  'elk.padding': '[top=30, left=30, bottom=30, right=30]',
};

function nodeHeight(node: GraphNode): number {
  if (node.step.type === 'DECISION') {
    return Math.max(70, Object.keys(node.step.conditionalNextSteps).length * 28);
  }
  if (node.step.type === 'PARALLEL_GATEWAY') {
    return Math.max(70, node.step.parallelNextSteps.length * 28);
  }
  return 70;
}

function nodePorts(node: GraphNode): ElkPort[] {
  const ports: ElkPort[] = [
    { id: `${node.id}__in`, properties: { 'port.side': 'WEST' } },
  ];
  if (node.step.type === 'DECISION') {
    Object.keys(node.step.conditionalNextSteps).forEach((_, i) => {
      ports.push({ id: `${node.id}__branch_${i}`, properties: { 'port.side': 'EAST' } });
    });
  } else if (node.step.type === 'PARALLEL_GATEWAY') {
    node.step.parallelNextSteps.forEach((_, i) => {
      ports.push({ id: `${node.id}__parallel_${i}`, properties: { 'port.side': 'EAST' } });
    });
  } else if (node.step.type !== 'END') {
    ports.push({ id: `${node.id}__out`, properties: { 'port.side': 'EAST' } });
  }
  return ports;
}

function elkSourcePort(edge: GraphEdge, source: GraphNode): string {
  if (edge.variant.kind === 'conditional' && source.step.type === 'DECISION') {
    const idx = Object.keys(source.step.conditionalNextSteps).indexOf(edge.variant.expression);
    return `${edge.from}__branch_${idx >= 0 ? idx : 0}`;
  }
  if (edge.variant.kind === 'parallel' && edge.sourceHandle) {
    const i = edge.sourceHandle.replace('parallel:', '');
    return `${edge.from}__parallel_${i}`;
  }
  return `${edge.from}__out`;
}

export async function layoutWithElk(
  nodes: GraphNode[],
  edges: GraphEdge[],
): Promise<Record<StepId, { x: number; y: number }>> {
  if (nodes.length === 0) return {};

  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  const graph: ElkNode = {
    id: 'root',
    layoutOptions: ELK_OPTIONS,
    children: nodes.map((n) => ({
      id: n.id,
      width: NODE_WIDTH,
      height: nodeHeight(n),
      ports: nodePorts(n),
      properties: { 'elk.portConstraints': 'FIXED_SIDE' },
    })),
    edges: edges
      .filter((e) => nodeById.has(e.from) && nodeById.has(e.to))
      .map(
        (e): ElkExtendedEdge => ({
          id: e.id,
          sources: [elkSourcePort(e, nodeById.get(e.from)!)],
          targets: [`${e.to}__in`],
        }),
      ),
  };

  const laid = await elk.layout(graph);

  const positions: Record<StepId, { x: number; y: number }> = {};
  for (const child of laid.children ?? []) {
    if (child.x != null && child.y != null) {
      positions[child.id] = { x: child.x, y: child.y };
    }
  }
  return positions;
}
```

- [ ] **Step 3.4: Run layout tests — expect all passing**

```bash
pnpm test tests/unit/layout.test.ts -- --reporter=verbose 2>&1 | tail -20
```

Expected: all 5 tests pass.

- [ ] **Step 3.5: Run full test suite — expect all passing**

```bash
pnpm test -- --reporter=verbose 2>&1 | tail -30
```

Expected: all tests pass. (The old dagre import is now gone from layout.ts so no unused-import errors.)

- [ ] **Step 3.6: Commit**

```bash
git add src/canvas/layout.ts tests/unit/layout.test.ts
git commit -m "feat: rewrite layout engine with ELK (port-aware, crossing minimisation)"
```

---

## Task 4: Switch edges to SmoothStep routing

**Files:**
- Modify: `src/canvas/edges/EdgeShell.tsx`

- [ ] **Step 4.1: Replace getBezierPath with getSmoothStepPath**

In `src/canvas/edges/EdgeShell.tsx`:

1. Change the import at the top from:
```typescript
import {
  BaseEdge,
  EdgeLabelRenderer,
  type EdgeProps,
  MarkerType,
  getBezierPath,
} from '@xyflow/react';
```
to:
```typescript
import {
  BaseEdge,
  EdgeLabelRenderer,
  type EdgeProps,
  MarkerType,
  getSmoothStepPath,
} from '@xyflow/react';
```

2. Change the path calculation:
```typescript
  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 10,
  });
```

(Replace the `getBezierPath({ ... })` call with the above.)

- [ ] **Step 4.2: Verify typecheck passes**

```bash
pnpm typecheck 2>&1 | tail -10
```

Expected: `Found 0 errors.` (or no output if already clean).

- [ ] **Step 4.3: Commit**

```bash
git add src/canvas/edges/EdgeShell.tsx
git commit -m "feat: switch edge routing to SmoothStep (orthogonal, no crossings)"
```

---

## Task 5: Async layout in Canvas.tsx

**Files:**
- Modify: `src/canvas/Canvas.tsx`

- [ ] **Step 5.1: Update mapToFlowEdges to propagate sourceHandle**

In `src/canvas/Canvas.tsx`, update `mapToFlowEdges`. Change the `base` object construction to include `sourceHandle`:

```typescript
function mapToFlowEdges(edges: GraphEdge[], nodes: GraphNode[]): Edge[] {
  const byId = new Map(nodes.map((n) => [n.id, n.step]));
  return edges.map((e) => {
    const base: Edge = {
      id: e.id,
      source: e.from,
      target: e.to,
      type: e.variant.kind,
      sourceHandle: e.sourceHandle ?? null,
      data: {},
    };
    if (e.variant.kind === 'conditional') {
      return { ...base, data: { expression: e.variant.expression } };
    }
    if (e.variant.kind === 'boundary') {
      const source = byId.get(e.from);
      const evt = source ? boundaryEventFor(source, e.variant.index) : undefined;
      return {
        ...base,
        data: evt
          ? { duration: evt.duration, interrupting: evt.interrupting }
          : { interrupting: false },
      };
    }
    return base;
  });
}
```

- [ ] **Step 5.2: Replace synchronous layout useMemo with async useEffect + useState**

First, add `useState` to the React import in `src/canvas/Canvas.tsx`. The current line is:
```typescript
import { type DragEvent, type ReactNode, useCallback, useEffect, useMemo } from 'react';
```
Change to:
```typescript
import { type DragEvent, type ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
```

In `CanvasInner`, find and **remove** this block:

```typescript
  const layout = useMemo(() => {
    if (nodes.length === 0) return {};
    const missing = nodes.some((n) => !(n.id in storedLayout));
    return missing ? { ...layoutLeftToRight(nodes, edges), ...storedLayout } : storedLayout;
  }, [nodes, edges, storedLayout]);
```

Replace with:

```typescript
  const [elkLayout, setElkLayout] = useState<Record<string, { x: number; y: number }>>({});

  useEffect(() => {
    if (nodes.length === 0) {
      setElkLayout({});
      return;
    }
    const missing = nodes.some((n) => !(n.id in storedLayout) && !(n.id in elkLayout));
    if (!missing) return;
    layoutWithElk(nodes, edges).then(setElkLayout);
  }, [nodes, edges, storedLayout, elkLayout]);

  const layout = useMemo(
    () => ({ ...elkLayout, ...storedLayout }),
    [elkLayout, storedLayout],
  );
```

- [ ] **Step 5.3: Update the import in Canvas.tsx**

Change:
```typescript
import { layoutLeftToRight } from './layout';
```
to:
```typescript
import { layoutWithElk } from './layout';
```

- [ ] **Step 5.4: Typecheck**

```bash
pnpm typecheck 2>&1 | tail -15
```

Expected: `Found 0 errors.`

If you see `'layoutLeftToRight' is not defined` or similar, search Canvas.tsx for any remaining reference to `layoutLeftToRight` and remove it.

- [ ] **Step 5.5: Run full test suite**

```bash
pnpm test -- --reporter=verbose 2>&1 | tail -20
```

Expected: all tests pass.

- [ ] **Step 5.6: Commit**

```bash
git add src/canvas/Canvas.tsx
git commit -m "feat: async ELK layout in Canvas, propagate sourceHandle to ReactFlow edges"
```

---

## Task 6: Async Tidy Layout in Toolbar.tsx

**Files:**
- Modify: `src/panels/Toolbar.tsx`

- [ ] **Step 6.1: Update handleTidy to async**

In `src/panels/Toolbar.tsx`:

1. Change the import:
```typescript
import { layoutLeftToRight } from '@/canvas/layout';
```
to:
```typescript
import { layoutWithElk } from '@/canvas/layout';
```

2. Replace `handleTidy`:

```typescript
  async function handleTidy(): Promise<void> {
    const def = useWorkflowStore.getState().definition;
    const positions = await layoutWithElk(toNodes(def), toEdges(def));
    useWorkflowStore.setState({ layout: positions });
    setTimeout(() => fitView({ duration: 250, padding: 0.2 }), 50);
  }
```

- [ ] **Step 6.2: Verify toNodes/toEdges are imported**

`Toolbar.tsx` must import `toNodes` and `toEdges` from `@/domain/graph`. Check the imports at the top. If missing, add:

```typescript
import { toEdges, toNodes } from '@/domain/graph';
```

- [ ] **Step 6.3: Typecheck**

```bash
pnpm typecheck 2>&1 | tail -15
```

Expected: `Found 0 errors.`

- [ ] **Step 6.4: Run full test suite**

```bash
pnpm test -- --reporter=verbose 2>&1 | tail -20
```

Expected: all tests pass.

- [ ] **Step 6.5: Commit**

```bash
git add src/panels/Toolbar.tsx
git commit -m "feat: async Tidy Layout using ELK (replaces full layout, clears manual positions)"
```

---

## Task 7: Final verification

- [ ] **Step 7.1: Full typecheck**

```bash
pnpm typecheck 2>&1
```

Expected: `Found 0 errors.` If any errors, fix them before continuing.

- [ ] **Step 7.2: Full test suite with coverage**

```bash
pnpm test -- --coverage 2>&1 | tail -30
```

Expected: all tests pass, coverage thresholds met (80% statements/branches/functions/lines).

- [ ] **Step 7.3: Confirm dagre is gone**

```bash
grep -r "dagre\|layoutLeftToRight\|tidyLayout" src/ --include="*.ts" --include="*.tsx"
```

Expected: no output (zero references to old layout functions or dagre).

- [ ] **Step 7.4: Confirm elkjs is used**

```bash
grep -r "elkjs\|layoutWithElk" src/ --include="*.ts" --include="*.tsx"
```

Expected: references in `src/canvas/layout.ts`, `src/canvas/Canvas.tsx`, `src/panels/Toolbar.tsx`.

- [ ] **Step 7.5: Final commit**

```bash
git add -A
git status  # confirm nothing unintended is staged
git commit -m "chore: cleanup — verify ELK migration complete" --allow-empty
```

(Use `--allow-empty` only if there's truly nothing left to stage. If there are untracked changes, stage and commit them properly instead.)
