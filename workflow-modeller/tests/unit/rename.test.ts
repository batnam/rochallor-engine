import { renameStepId } from '@/domain/rename';
import type { WorkflowDefinition } from '@/domain/types';
import { describe, expect, it } from 'vitest';

function makeDef(): WorkflowDefinition {
  return {
    id: 'demo',
    name: 'demo',
    steps: [
      {
        id: 'start',
        name: 'start',
        type: 'SERVICE_TASK',
        nextStep: 'target-step', // (a) plain nextStep
      },
      {
        id: 'router',
        name: 'router',
        type: 'DECISION',
        conditionalNextSteps: {
          'a == 1': 'target-step', // (b) conditionalNextSteps value
          'a == 2': 'end-ok',
        },
      },
      {
        id: 'fork',
        name: 'fork',
        type: 'PARALLEL_GATEWAY',
        parallelNextSteps: ['target-step', 'sibling'], // (c) parallelNextSteps
        joinStep: 'target-step', // (d) joinStep
      },
      {
        id: 'sibling',
        name: 'sibling',
        type: 'SERVICE_TASK',
        nextStep: 'target-step',
        boundaryEvents: [
          {
            type: 'TIMER',
            duration: 'PT1H',
            interrupting: false,
            targetStepId: 'target-step', // (e) boundaryEvents[].targetStepId
          },
        ],
      },
      {
        id: 'target-step',
        name: 'Target',
        type: 'JOIN_GATEWAY',
        nextStep: 'end-ok',
      },
      { id: 'end-ok', name: 'End', type: 'END' },
    ],
  };
}

describe('renameStepId', () => {
  it('rewrites references across all five call sites', () => {
    const renamed = renameStepId(makeDef(), 'target-step', 'new-target');

    // Renamed step appears by its new id, not the old one.
    expect(renamed.steps.find((s) => s.id === 'new-target')).toBeDefined();
    expect(renamed.steps.find((s) => s.id === 'target-step')).toBeUndefined();

    // (a) SERVICE_TASK.nextStep
    const start = renamed.steps.find((s) => s.id === 'start');
    expect(start?.type).toBe('SERVICE_TASK');
    if (start?.type === 'SERVICE_TASK') expect(start.nextStep).toBe('new-target');

    // (b) DECISION.conditionalNextSteps value — insertion order preserved
    const router = renamed.steps.find((s) => s.id === 'router');
    if (router?.type === 'DECISION') {
      expect(Object.entries(router.conditionalNextSteps)).toEqual([
        ['a == 1', 'new-target'],
        ['a == 2', 'end-ok'],
      ]);
    } else {
      throw new Error('router missing');
    }

    // (c) PARALLEL_GATEWAY.parallelNextSteps + (d) joinStep
    const fork = renamed.steps.find((s) => s.id === 'fork');
    if (fork?.type === 'PARALLEL_GATEWAY') {
      expect(fork.parallelNextSteps).toEqual(['new-target', 'sibling']);
      expect(fork.joinStep).toBe('new-target');
    } else {
      throw new Error('fork missing');
    }

    // (e) boundaryEvents[].targetStepId
    const sibling = renamed.steps.find((s) => s.id === 'sibling');
    if (sibling?.type === 'SERVICE_TASK' && sibling.boundaryEvents) {
      expect(sibling.boundaryEvents[0]?.targetStepId).toBe('new-target');
      expect(sibling.nextStep).toBe('new-target');
    } else {
      throw new Error('sibling boundary missing');
    }
  });

  it('rejects rename when the new id collides with an existing step', () => {
    expect(() => renameStepId(makeDef(), 'target-step', 'end-ok')).toThrow(/already exists/);
  });

  it('is a no-op when oldId === newId', () => {
    const def = makeDef();
    const renamed = renameStepId(def, 'start', 'start');
    expect(renamed).toBe(def);
  });

  it('returns a fresh definition (does not mutate the input)', () => {
    const def = makeDef();
    const originalJson = JSON.stringify(def);
    renameStepId(def, 'target-step', 'new-target');
    expect(JSON.stringify(def)).toBe(originalJson);
  });
});
