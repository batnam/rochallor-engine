import type { DecisionTableStep } from '@/domain/types';
import { DecisionTableForm } from '@/panels/property-forms/DecisionTableForm';
import { useWorkflowStore } from '@/store/workflowStore';
import { cleanup, fireEvent, render } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type Rule = DecisionTableStep['decisionTable']['rules'][number];

function seedDt(rules: Rule[] = []): string {
  const store = useWorkflowStore.getState();
  const id = store.addStep({ type: 'DECISION_TABLE', id: 'dt' });
  store.addStep({ type: 'END', id: 'target-a' });
  store.addStep({ type: 'END', id: 'target-b' });
  store.updateStepProperty(id, 'decisionTable', { rules });
  return id;
}

function currentDt(): DecisionTableStep {
  const step = useWorkflowStore.getState().definition.steps.find((s) => s.id === 'dt') as
    | DecisionTableStep
    | undefined;
  if (!step) throw new Error('dt step missing');
  return step;
}

function renderForm(): ReturnType<typeof render> {
  const step = currentDt();
  return render(<DecisionTableForm step={step} />);
}

describe('DecisionTableForm', () => {
  beforeEach(() => {
    useWorkflowStore.getState().reset();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('renders empty-state hints when the table has no rules and no columns', () => {
    seedDt();
    const { getByText } = renderForm();
    expect(getByText('No input columns. Add one below.')).toBeTruthy();
    expect(getByText('No rules yet. Add one below.')).toBeTruthy();
  });

  it('renders a hit-policy selector that defaults to Unique', () => {
    seedDt();
    const { getByLabelText } = renderForm();
    const select = getByLabelText('Hit policy') as HTMLSelectElement;
    expect(select).toBeTruthy();
    expect(select.value).toBe('U');
  });

  it('renders a nextStep picker (linear successor)', () => {
    seedDt();
    const { getByLabelText } = renderForm();
    expect(getByLabelText('Next step')).toBeTruthy();
  });

  it('does NOT render a per-row step picker', () => {
    seedDt([{ when: {}, outputs: {} }]);
    const { container } = renderForm();
    const ruleRow = container.querySelector('.wm-dt-rule-row');
    expect(ruleRow).toBeTruthy();
    // The rule row should only contain inputs (when/outputs cells) and the row-action buttons.
    // No <select> should live inside an individual rule row.
    expect(ruleRow?.querySelector('select')).toBeNull();
  });

  it('appends an empty rule when "+ add rule" is clicked', async () => {
    seedDt();
    const { getByText } = renderForm();
    const user = userEvent.setup();
    await user.click(getByText('+ add rule'));
    expect(currentDt().decisionTable.rules).toEqual([{ when: {}, outputs: {} }]);
  });

  it('seeds new rules with the existing input columns as wildcard cells', async () => {
    seedDt([{ when: { score: 'value >= 650' }, outputs: {} }]);
    const { getByText } = renderForm();
    const user = userEvent.setup();
    await user.click(getByText('+ add rule'));
    const rules = currentDt().decisionTable.rules;
    expect(rules).toHaveLength(2);
    expect(rules[1]?.when).toEqual({ score: '' });
  });

  it('adds an input column to every existing rule when prompted', async () => {
    seedDt([{ when: {}, outputs: {} }]);
    vi.spyOn(window, 'prompt').mockReturnValueOnce('region');
    const { getByText } = renderForm();
    const user = userEvent.setup();
    await user.click(getByText('+ add input'));
    expect(currentDt().decisionTable.rules[0]?.when).toEqual({ region: '' });
  });

  it('atomically renames an input column across all rules', async () => {
    seedDt([
      { when: { score: 'value >= 650' }, outputs: {} },
      { when: { score: 'value < 650' }, outputs: {} },
    ]);
    vi.spyOn(window, 'prompt').mockReturnValueOnce('creditScore');
    const { getByLabelText } = renderForm();
    const user = userEvent.setup();
    await user.click(getByLabelText('Rename score'));
    const rules = currentDt().decisionTable.rules;
    expect(rules[0]?.when).toEqual({ creditScore: 'value >= 650' });
    expect(rules[1]?.when).toEqual({ creditScore: 'value < 650' });
  });

  it('removes an input column from every rule', async () => {
    seedDt([{ when: { score: 'value >= 650', region: 'value == "NA"' }, outputs: {} }]);
    const { getByLabelText } = renderForm();
    const user = userEvent.setup();
    await user.click(getByLabelText('Remove region'));
    expect(currentDt().decisionTable.rules[0]?.when).toEqual({ score: 'value >= 650' });
  });

  it('moves rule 2 up to position 1 when its ↑ button is clicked', async () => {
    seedDt([
      { when: { a: 'value == 1' }, outputs: { tier: 'A' } },
      { when: { a: 'value == 2' }, outputs: { tier: 'B' } },
    ]);
    const { getAllByLabelText } = renderForm();
    const user = userEvent.setup();
    await user.click(getAllByLabelText('Move up')[1] as HTMLElement);
    const rules = currentDt().decisionTable.rules;
    expect(rules[0]?.outputs?.tier).toBe('B');
    expect(rules[1]?.outputs?.tier).toBe('A');
  });

  it('parses output cell values: literal "GOLD" stays a string, 0.5 becomes number, ${...} stays raw', () => {
    seedDt([{ when: {}, outputs: { tier: '' } }]);
    const { container } = renderForm();
    const cells = container.querySelectorAll<HTMLInputElement>('.wm-dt-cell--output');
    expect(cells.length).toBe(1);
    const cell = cells[0] as HTMLInputElement;

    fireEvent.change(cell, { target: { value: '"GOLD"' } });
    fireEvent.blur(cell);
    expect(currentDt().decisionTable.rules[0]?.outputs?.tier).toBe('GOLD');

    fireEvent.change(cell, { target: { value: '0.5' } });
    fireEvent.blur(cell);
    expect(currentDt().decisionTable.rules[0]?.outputs?.tier).toBe(0.5);

    fireEvent.change(cell, { target: { value: '${baseFee * 1.1}' } });
    fireEvent.blur(cell);
    expect(currentDt().decisionTable.rules[0]?.outputs?.tier).toBe('${baseFee * 1.1}');
  });

  it('changing the hit-policy selector commits through the store', async () => {
    seedDt();
    const { getByLabelText } = renderForm();
    const user = userEvent.setup();
    const select = getByLabelText('Hit policy') as HTMLSelectElement;
    await user.selectOptions(select, 'C+');
    expect(currentDt().hitPolicy).toBe('C+');
  });

  it('changing the nextStep picker commits through the store', async () => {
    seedDt();
    const { getByLabelText } = renderForm();
    const user = userEvent.setup();
    await user.selectOptions(getByLabelText('Next step') as HTMLSelectElement, 'target-a');
    expect(currentDt().nextStep).toBe('target-a');
  });

  it('undo restores prior table state via the zundo temporal store', () => {
    seedDt([{ when: { score: 'value >= 650' }, outputs: { tier: 'GOLD' } }]);
    const before = currentDt().decisionTable.rules;
    useWorkflowStore.getState().updateStepProperty('dt', 'decisionTable', {
      rules: [{ when: {}, outputs: {} }],
    });
    expect(currentDt().decisionTable.rules).not.toEqual(before);
    useWorkflowStore.temporal.getState().undo();
    expect(currentDt().decisionTable.rules).toEqual(before);
  });
});
