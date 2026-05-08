import type { Step, WorkflowDefinition } from '@/domain/types';
import { findReferences, rewriteRefs } from '@/panels/DeleteStepDialog';
import { describe, expect, it } from 'vitest';

function dtDef(): WorkflowDefinition {
  return {
    id: 'wf',
    name: 'wf',
    steps: [
      {
        id: 'dt',
        name: 'DT',
        type: 'DECISION_TABLE',
        hitPolicy: 'F',
        nextStep: 'a',
        decisionTable: {
          rules: [{ when: { x: 'value == 1' }, outputs: { tier: 'GOLD' } }],
        },
      },
      { id: 'a', name: 'A', type: 'END' },
      { id: 'b', name: 'B', type: 'END' },
    ],
  };
}

describe('findReferences for DECISION_TABLE', () => {
  it('lists DECISION_TABLE.nextStep as an inbound reference to the target', () => {
    const refs = findReferences(dtDef(), 'a');
    expect(refs).toEqual(['dt.nextStep']);
  });

  it('returns an empty list when no DT field references the step', () => {
    expect(findReferences(dtDef(), 'b')).toEqual([]);
  });
});

describe('rewriteRefs for DECISION_TABLE', () => {
  it('rewrites nextStep when it equals oldId', () => {
    const dt = dtDef().steps[0] as Extract<Step, { type: 'DECISION_TABLE' }>;
    const rewritten = rewriteRefs(dt, 'a', 'a-renamed') as Extract<
      Step,
      { type: 'DECISION_TABLE' }
    >;
    expect(rewritten.nextStep).toBe('a-renamed');
  });

  it('returns the step unchanged when oldId is not referenced', () => {
    const dt = dtDef().steps[0] as Extract<Step, { type: 'DECISION_TABLE' }>;
    const rewritten = rewriteRefs(dt, 'unrelated', 'whatever') as Extract<
      Step,
      { type: 'DECISION_TABLE' }
    >;
    expect(rewritten.nextStep).toBe('a');
  });
});
