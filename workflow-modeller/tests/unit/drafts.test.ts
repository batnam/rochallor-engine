import type { WorkflowDefinition } from '@/domain/types';
import {
  DRAFTS_LIMIT,
  _clearAllDrafts,
  deleteDraft,
  getDraft,
  listDrafts,
  saveDraft,
} from '@/io/drafts';
import { beforeEach, describe, expect, it } from 'vitest';

function defOf(name: string, stepCount = 1): WorkflowDefinition {
  return {
    id: 'wf',
    name,
    steps: Array.from({ length: stepCount }, (_, i) => ({
      id: `s${i}`,
      name: `Step ${i}`,
      type: 'END',
    })),
  } as WorkflowDefinition;
}

const blank = { layout: {}, edgeHandles: {} };

beforeEach(() => {
  _clearAllDrafts();
});

describe('saveDraft', () => {
  it('persists a draft and lists it back', () => {
    const r = saveDraft({ name: 'My draft', definition: defOf('My draft', 2), ...blank });
    expect(r.ok).toBe(true);
    const summaries = listDrafts();
    expect(summaries).toHaveLength(1);
    expect(summaries[0]?.name).toBe('My draft');
    expect(summaries[0]?.stepCount).toBe(2);
  });

  it('rejects an empty name with NAME_REQUIRED', () => {
    const r = saveDraft({ name: '   ', definition: defOf('x'), ...blank });
    expect(r).toEqual({ ok: false, reason: 'NAME_REQUIRED' });
    expect(listDrafts()).toEqual([]);
  });

  it('rejects with CAP_REACHED once DRAFTS_LIMIT entries exist', () => {
    for (let i = 0; i < DRAFTS_LIMIT; i++) {
      const r = saveDraft({ name: `d${i}`, definition: defOf(`d${i}`), ...blank });
      expect(r.ok).toBe(true);
    }
    const r = saveDraft({ name: 'overflow', definition: defOf('overflow'), ...blank });
    expect(r).toEqual({ ok: false, reason: 'CAP_REACHED' });
    expect(listDrafts()).toHaveLength(DRAFTS_LIMIT);
  });

  it('assigns a unique id and a savedAt timestamp', () => {
    const a = saveDraft({ name: 'a', definition: defOf('a'), ...blank });
    const b = saveDraft({ name: 'b', definition: defOf('b'), ...blank });
    if (!a.ok || !b.ok) throw new Error('save failed');
    expect(a.draft.id).not.toBe(b.draft.id);
    expect(a.draft.savedAt).toBeGreaterThan(0);
  });
});

describe('getDraft', () => {
  it('returns the full draft body including definition + layout', () => {
    const layout = { s0: { x: 10, y: 20 } };
    const r = saveDraft({ name: 'a', definition: defOf('a'), layout, edgeHandles: {} });
    if (!r.ok) throw new Error('save failed');

    const got = getDraft(r.draft.id);
    expect(got).not.toBeNull();
    expect(got?.definition.steps).toHaveLength(1);
    expect(got?.layout).toEqual(layout);
  });

  it('returns null for an unknown id', () => {
    expect(getDraft('nope')).toBeNull();
  });
});

describe('deleteDraft', () => {
  it('removes the draft and leaves the others', () => {
    const a = saveDraft({ name: 'a', definition: defOf('a'), ...blank });
    const b = saveDraft({ name: 'b', definition: defOf('b'), ...blank });
    if (!a.ok || !b.ok) throw new Error('save failed');

    deleteDraft(a.draft.id);

    const remaining = listDrafts();
    expect(remaining).toHaveLength(1);
    expect(remaining[0]?.id).toBe(b.draft.id);
  });

  it('frees a slot so a new save can succeed after CAP_REACHED', () => {
    for (let i = 0; i < DRAFTS_LIMIT; i++) {
      saveDraft({ name: `d${i}`, definition: defOf(`d${i}`), ...blank });
    }
    const oldest = listDrafts()[0];
    if (!oldest) throw new Error('expected drafts');
    deleteDraft(oldest.id);

    const r = saveDraft({ name: 'fresh', definition: defOf('fresh'), ...blank });
    expect(r.ok).toBe(true);
    expect(listDrafts()).toHaveLength(DRAFTS_LIMIT);
  });
});
