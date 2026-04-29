# ELK Layout Engine — Design Spec

**Date:** 2026-04-29  
**Status:** Approved

## Problem

The workflow canvas is visually cluttered:

1. **Bezier curves cross** at Decision nodes — branches going up and branches going down produce an X-shaped tangle because `getBezierPath` does not guarantee non-crossing paths.
2. **Nodes are too close together** — dagre uses `nodesep: 40px` and `ranksep: 80px`, leaving very little breathing room.
3. **dagre is port-unaware** — it treats Decision/Parallel nodes as single-output nodes, so it cannot distribute branch targets sensibly around the node's actual handle positions.
4. **NODE_HEIGHT = 56px** underestimates actual rendered height, worsening spacing calculations.

## Solution

Replace dagre with **ELK (Eclipse Layout Kernel)** via the `elkjs` npm package. ELK:

- Understands node ports, so Decision and Parallel branches are assigned specific exit positions.
- Uses the `layered` algorithm with LAYER_SWEEP crossing minimization.
- Produces orthogonal (right-angle) edge routing natively — combined with ReactFlow's `getSmoothStepPath` for the visual path.

"Tidy Layout" discards all manual positions and runs a full ELK re-layout (user-chosen behaviour).

---

## Architecture

### Data flow (after change)

```
WorkflowStore (nodes + edges w/ sourceHandle)
        │
        ▼ useEffect (async, fires when nodes missing positions)
layoutWithElk(nodes, edges) → Promise<Record<StepId, {x,y}>>
        │
        ▼ setElkLayout(result)
local elkLayout state   ← storedLayout (manual drags) always overrides
        │
        ▼
ReactFlow nodes (positioned) + edges (with sourceHandle set)
```

### Tidy Layout flow

```
handleTidy (async)
  → layoutWithElk(all nodes, all edges)
  → useWorkflowStore.setState({ layout: result })   // replaces everything
  → fitView({ duration: 250 })
```

---

## ELK Configuration

```typescript
const ELK_OPTIONS = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.spacing.nodeNode': '80',
  'elk.layered.spacing.nodeNodeBetweenLayers': '140',
  'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
  'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
  'elk.edgeRouting': 'ORTHOGONAL',
  'elk.padding': '[top=30, left=30, bottom=30, right=30]',
};
```

### Dynamic node height

| Node type | Height formula |
|---|---|
| Default | `70px` |
| `DECISION` (n branches) | `max(70, n × 28)px` |
| `PARALLEL_GATEWAY` (n branches) | `max(70, n × 28)px` |

Node width stays `180px` for all types.

### Port model

Every node gets one WEST port (`in`) and one or more EAST ports. Port IDs match ReactFlow Handle IDs exactly so no node component changes are needed.

| Node type | EAST ports |
|---|---|
| Default (single output) | `out` |
| `DECISION` | `branch:<expression>` per branch |
| `PARALLEL_GATEWAY` | `parallel:<i>` per branch (i = index in `parallelNextSteps` array) |
| `JOIN_GATEWAY` | `out` |
| `END` | _(none)_ |

ELK distributes EAST ports evenly on the node's right side within the `FIXED_SIDE` port constraint.

---

## Edge routing — getSmoothStepPath

`EdgeShell.tsx` switches from `getBezierPath` to `getSmoothStepPath` with `borderRadius: 10`. This applies to all edge types: sequential, conditional, parallel, join-out, boundary. Orthogonal routing eliminates crossings visually even for paths that ELK couldn't fully separate.

---

## Async handling in Canvas.tsx

```typescript
// Replaces the synchronous useMemo layout block
const [elkLayout, setElkLayout] = useState<Positions>({});

useEffect(() => {
  if (nodes.length === 0) { setElkLayout({}); return; }
  const missing = nodes.some(n => !(n.id in storedLayout));
  if (!missing) return;
  layoutWithElk(nodes, edges).then(setElkLayout);
}, [nodes, edges, storedLayout]);

const layout = useMemo(() => ({
  ...elkLayout,
  ...storedLayout,   // manual drag positions always win
}), [elkLayout, storedLayout]);
```

ELK completes in ~10–20 ms for typical workflows, so no loading spinner is needed. `fitView` continues to fire on `source` changes as before — by that point ELK has already resolved.

---

## Files changed (7)

| File | Change |
|---|---|
| `package.json` | Add `elkjs` |
| `src/domain/types.ts` | Add `sourceHandle?: string` to `GraphEdge` |
| `src/domain/graph.ts` | Set `sourceHandle` for DECISION (`branch:<expr>`) and PARALLEL (`parallel:<i>`) edges |
| `src/canvas/layout.ts` | Full rewrite: `layoutWithElk(nodes, edges): Promise<Positions>` using ELK |
| `src/canvas/edges/EdgeShell.tsx` | `getBezierPath` → `getSmoothStepPath(borderRadius: 10)` |
| `src/canvas/Canvas.tsx` | Async layout pattern; propagate `sourceHandle` to ReactFlow edge objects |
| `src/panels/Toolbar.tsx` | `handleTidy` becomes async; awaits `layoutWithElk` then sets store layout |

No changes to node components (`NodeShell`, `DecisionNode`, etc.) or the store schema.

---

## Out of scope

- Web Worker for ELK (acceptable future optimisation; ELK is fast enough synchronously for typical workflow sizes)
- Per-node manual pin (lock position) — existing drag-and-store behaviour is unchanged
- Edge label repositioning — labels stay at midpoint as implemented by `EdgeLabelRenderer`
