import { readFileSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { indexSteps, toEdges, toNodes } from '@/domain/graph';
import { zWorkflowDefinition } from '@/domain/schema';
import type { WorkflowDefinition } from '@/domain/types';
import { describe, expect, it } from 'vitest';

const VALID_DIR = resolve(__dirname, '../fixtures/valid');

function loadFixtures(): Array<{ name: string; def: WorkflowDefinition }> {
  const out: Array<{ name: string; def: WorkflowDefinition }> = [];
  for (const file of readdirSync(VALID_DIR)) {
    if (!file.endsWith('.json')) continue;
    const raw = JSON.parse(readFileSync(join(VALID_DIR, file), 'utf8'));
    const parsed = zWorkflowDefinition.parse(raw);
    out.push({ name: file, def: parsed });
  }
  return out;
}

describe('toNodes', () => {
  it('produces one node per step, in order, with entry flag on index 0', () => {
    for (const { name, def } of loadFixtures()) {
      const nodes = toNodes(def);
      expect(nodes, name).toHaveLength(def.steps.length);
      expect(nodes[0]?.isEntry, name).toBe(true);
      for (let i = 1; i < nodes.length; i++) {
        expect(nodes[i]?.isEntry, `${name} step ${i}`).toBe(false);
      }
      const index = indexSteps(def);
      for (const node of nodes) {
        expect(index.get(node.id), `${name} node ${node.id}`).toBe(node.step);
      }
    }
  });
});

describe('toEdges', () => {
  it('loan-application-full: produces expected variant counts', () => {
    const def = loadFixtures().find((f) => f.name === 'loan-application-full.json')?.def;
    expect(def).toBeDefined();
    if (!def) return;

    const edges = toEdges(def);
    const byKind = groupByKind(edges);

    // 14 steps with these outgoing flows:
    // - 5 SERVICE_TASK → sequential (validate + credit + fraud + escalate + auto-approve)
    // - 1 PARALLEL_GATEWAY → 2 parallel
    // - 1 JOIN_GATEWAY → 1 join-out
    // - 1 TRANSFORMATION → 1 sequential
    // - 2 DECISION → 3 + 2 conditional
    // - 1 USER_TASK → 1 sequential + 1 boundary
    // - 3 END → 0
    expect(byKind.sequential).toBe(7);
    expect(byKind.conditional).toBe(5);
    expect(byKind.parallel).toBe(2);
    expect(byKind['join-out']).toBe(1);
    expect(byKind.boundary).toBe(1);
  });

  it('loan-disbursement-workflow: produces expected variant counts', () => {
    const def = loadFixtures().find((f) => f.name === 'loan-disbursement-workflow.json')?.def;
    expect(def).toBeDefined();
    if (!def) return;

    const edges = toEdges(def);
    const byKind = groupByKind(edges);

    // 11 steps:
    // - 4 SERVICE_TASK → 4 sequential (notify-overdue, prepare, transfer, notify)
    // - 1 TRANSFORMATION → 1 sequential
    // - 1 USER_TASK → 1 sequential + 1 boundary
    // - 2 DECISION → 2 + 2 conditional
    // - 3 END → 0
    expect(byKind.sequential).toBe(6);
    expect(byKind.conditional).toBe(4);
    expect(byKind.boundary).toBe(1);
    expect(byKind.parallel ?? 0).toBe(0);
  });

  it('every edge resolves from/to an existing step', () => {
    for (const { name, def } of loadFixtures()) {
      const ids = new Set(def.steps.map((s) => s.id));
      for (const edge of toEdges(def)) {
        expect(ids.has(edge.from), `${name}: edge.from "${edge.from}"`).toBe(true);
        expect(ids.has(edge.to), `${name}: edge.to "${edge.to}"`).toBe(true);
      }
    }
  });
});

function groupByKind(edges: ReturnType<typeof toEdges>): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const e of edges) counts[e.variant.kind] = (counts[e.variant.kind] ?? 0) + 1;
  return counts;
}
