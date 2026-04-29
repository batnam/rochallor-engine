import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { layoutWithElk } from '@/canvas/layout';
import { toEdges, toNodes } from '@/domain/graph';
import { zWorkflowDefinition } from '@/domain/schema';
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
