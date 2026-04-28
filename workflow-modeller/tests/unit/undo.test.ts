import type { WorkflowDefinition } from '@/domain/types';
import { useWorkflowStore } from '@/store/workflowStore';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

interface TemporalState {
  pastStates: unknown[];
  futureStates: unknown[];
  clear: () => void;
}

function temporal(): TemporalState {
  return (
    useWorkflowStore as unknown as { temporal: { getState: () => TemporalState } }
  ).temporal.getState();
}

const seed: WorkflowDefinition = {
  id: 'wf-test',
  name: 'Test',
  steps: [
    { id: 'a', name: 'A', type: 'SERVICE_TASK', nextStep: 'target' },
    {
      id: 'b',
      name: 'B',
      type: 'TRANSFORMATION',
      nextStep: 'target',
      transformations: { x: '1' },
    },
    { id: 'target', name: 'Target', type: 'END' },
    { id: 'replacement', name: 'Repl', type: 'END' },
  ],
};

describe('zundo temporal frames', () => {
  beforeEach(() => {
    useWorkflowStore.getState().reset();
    useWorkflowStore.setState({ definition: seed });
    temporal().clear();
  });

  afterEach(() => {
    useWorkflowStore.getState().reset();
    temporal().clear();
  });

  it('cascading rename records exactly one frame and updates every ref', () => {
    useWorkflowStore.getState().renameStepId('target', 'target-renamed');

    expect(temporal().pastStates).toHaveLength(1);
    const def = useWorkflowStore.getState().definition;
    expect(def.steps.find((s) => s.id === 'a')?.nextStep).toBe('target-renamed');
    expect(def.steps.find((s) => s.id === 'b')?.nextStep).toBe('target-renamed');
    expect(def.steps.find((s) => s.id === 'target')).toBeUndefined();
    expect(def.steps.find((s) => s.id === 'target-renamed')).toBeDefined();
  });

  it('delete-with-replacement records one frame', () => {
    useWorkflowStore.getState().deleteStep('target', 'replacement');
    expect(temporal().pastStates).toHaveLength(1);
  });

  it('setLayout does not pollute history', () => {
    useWorkflowStore.getState().setLayout('a', { x: 100, y: 200 });
    expect(temporal().pastStates).toHaveLength(0);
  });

  it('select does not pollute history', () => {
    useWorkflowStore.getState().select({ kind: 'step', id: 'a' });
    expect(temporal().pastStates).toHaveLength(0);
  });

  it('runValidation does not pollute history', () => {
    useWorkflowStore.getState().runValidation();
    expect(temporal().pastStates).toHaveLength(0);
  });

  it('setEngineConnection does not pollute history', () => {
    useWorkflowStore.getState().setEngineConnection({ baseUrl: 'http://x' });
    expect(temporal().pastStates).toHaveLength(0);
  });

  it('undo reverts a cascading rename atomically', () => {
    useWorkflowStore.getState().renameStepId('target', 'target-renamed');
    expect(temporal().pastStates).toHaveLength(1);

    (useWorkflowStore as unknown as { temporal: { getState: () => { undo: () => void } } }).temporal
      .getState()
      .undo();

    const def = useWorkflowStore.getState().definition;
    expect(def.steps.find((s) => s.id === 'target')).toBeDefined();
    expect(def.steps.find((s) => s.id === 'target-renamed')).toBeUndefined();
    expect(def.steps.find((s) => s.id === 'a')?.nextStep).toBe('target');
    expect(def.steps.find((s) => s.id === 'b')?.nextStep).toBe('target');
  });
});
