import { useWorkflowStore } from '@/store/workflowStore';
import { beforeEach, describe, expect, it } from 'vitest';

beforeEach(() => {
  useWorkflowStore.getState().reset();
});

describe('addStep', () => {
  it('appends a new step of the requested type with a unique id', () => {
    const id1 = useWorkflowStore.getState().addStep({ type: 'SERVICE_TASK' });
    const id2 = useWorkflowStore.getState().addStep({ type: 'SERVICE_TASK' });

    const steps = useWorkflowStore.getState().definition.steps;
    expect(steps).toHaveLength(2);
    expect(steps[0]?.id).toBe(id1);
    expect(steps[1]?.id).toBe(id2);
    expect(id1).not.toBe(id2);
  });

  it('marks the store dirty after an addition', () => {
    expect(useWorkflowStore.getState().dirty).toBe(false);
    useWorkflowStore.getState().addStep({ type: 'END' });
    expect(useWorkflowStore.getState().dirty).toBe(true);
  });

  it('honours an explicit id when it does not collide', () => {
    const id = useWorkflowStore.getState().addStep({ type: 'END', id: 'my-end' });
    expect(id).toBe('my-end');
  });

  it('appends a numeric suffix on id collisions', () => {
    useWorkflowStore.getState().addStep({ type: 'END', id: 'finish' });
    const second = useWorkflowStore.getState().addStep({ type: 'END', id: 'finish' });
    expect(second).toBe('finish-2');
  });
});

describe('deleteStep', () => {
  it('removes the step and scrubs references pointing to it', () => {
    const store = useWorkflowStore.getState();
    // Seed: a SERVICE_TASK whose nextStep is an END step we will remove.
    store.addStep({ type: 'SERVICE_TASK', id: 'svc' });
    store.addStep({ type: 'END', id: 'finish' });
    store.updateStepProperty('svc', 'nextStep', 'finish');

    store.deleteStep('finish');

    const steps = useWorkflowStore.getState().definition.steps;
    expect(steps.find((s) => s.id === 'finish')).toBeUndefined();
    const svc = steps.find((s) => s.id === 'svc');
    if (svc?.type === 'SERVICE_TASK') {
      expect(svc.nextStep).toBe('');
    } else {
      throw new Error('svc missing or wrong type');
    }
  });

  it('strips dangling boundaryEvents when the target is deleted', () => {
    const store = useWorkflowStore.getState();
    store.addStep({ type: 'USER_TASK', id: 'review' });
    store.addStep({ type: 'SERVICE_TASK', id: 'escalate' });
    store.updateStepProperty('review', 'boundaryEvents', [
      {
        type: 'TIMER',
        duration: 'PT1H',
        interrupting: false,
        targetStepId: 'escalate',
      },
    ]);

    store.deleteStep('escalate');

    const review = useWorkflowStore.getState().definition.steps.find((s) => s.id === 'review');
    if (review?.type === 'USER_TASK') {
      expect(review.boundaryEvents ?? []).toHaveLength(0);
    } else {
      throw new Error('review missing or wrong type');
    }
  });

  it('drops a DECISION branch whose target is deleted', () => {
    const store = useWorkflowStore.getState();
    store.addStep({ type: 'DECISION', id: 'router' });
    store.addStep({ type: 'END', id: 'ok' });
    store.addStep({ type: 'END', id: 'nope' });
    store.updateStepProperty('router', 'conditionalNextSteps', {
      'a == 1': 'ok',
      'a == 2': 'nope',
    });

    store.deleteStep('nope');

    const router = useWorkflowStore.getState().definition.steps.find((s) => s.id === 'router');
    if (router?.type === 'DECISION') {
      expect(Object.entries(router.conditionalNextSteps)).toEqual([['a == 1', 'ok']]);
    } else {
      throw new Error('router missing');
    }
  });
});

describe('renameStepId via store', () => {
  it('cascades the rename across references and marks dirty', () => {
    const store = useWorkflowStore.getState();
    store.addStep({ type: 'SERVICE_TASK', id: 'a' });
    store.addStep({ type: 'END', id: 'b' });
    store.updateStepProperty('a', 'nextStep', 'b');

    store.renameStepId('b', 'b-final');

    const steps = useWorkflowStore.getState().definition.steps;
    expect(steps.find((s) => s.id === 'b-final')).toBeDefined();
    const a = steps.find((s) => s.id === 'a');
    if (a?.type === 'SERVICE_TASK') expect(a.nextStep).toBe('b-final');
    else throw new Error('a missing');
    expect(useWorkflowStore.getState().dirty).toBe(true);
  });
});
