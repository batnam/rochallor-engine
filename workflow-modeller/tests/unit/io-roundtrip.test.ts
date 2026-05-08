import { readFileSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { validate } from '@/domain/validate';
import { exportJson } from '@/io/export';
import { importJson } from '@/io/import';
import { describe, expect, it } from 'vitest';

const VALID_DIR = resolve(__dirname, '../fixtures/valid');
const INVALID_DIR = resolve(__dirname, '../fixtures/invalid');

function listValidFixtures(): string[] {
  return readdirSync(VALID_DIR).filter((f) => f.endsWith('.json'));
}

describe('import → export round-trip', () => {
  it.each(listValidFixtures())('%s: import → export → import is structurally stable', (file) => {
    const original = readFileSync(join(VALID_DIR, file), 'utf8');
    const first = importJson(original);
    expect(first.ok, file).toBe(true);
    if (!first.ok) return;

    const serialised = exportJson(first.def);
    const second = importJson(serialised);
    expect(second.ok, `re-import ${file}`).toBe(true);
    if (!second.ok) return;

    expect(JSON.parse(serialised)).toEqual(JSON.parse(exportJson(second.def)));
  });

  it('preserves unknown top-level and step-level fields', () => {
    const synthetic = {
      id: 'demo',
      name: 'demo',
      futureTopLevelField: { arbitrary: true },
      steps: [
        {
          id: 'a',
          name: 'A',
          type: 'SERVICE_TASK',
          futureStepField: 'hello',
        },
        {
          id: 'b',
          name: 'B',
          type: 'END',
        },
      ],
    };
    const result = importJson(JSON.stringify(synthetic));
    expect(result.ok).toBe(true);
    if (!result.ok) return;

    expect(result.warnings.some((w) => w.includes('futureTopLevelField'))).toBe(true);
    expect(result.warnings.some((w) => w.includes('futureStepField'))).toBe(true);

    const rehydrated = JSON.parse(exportJson(result.def));
    expect(rehydrated.futureTopLevelField).toEqual({ arbitrary: true });
    expect(rehydrated.steps[0].futureStepField).toBe('hello');
  });

  it('rejects malformed JSON with a readable error', () => {
    const result = importJson('{ not json');
    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.errors[0]).toMatch(/Invalid JSON/);
  });

  it('rejects a definition missing required fields', () => {
    const result = importJson(JSON.stringify({ steps: [] }));
    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.errors.length).toBeGreaterThan(0);
  });
});

describe('DECISION_TABLE — round-trip and fixture-driven import (007 shape)', () => {
  it('decision-table-pricing.json: import → export is structurally identical to the source', () => {
    const original = readFileSync(join(VALID_DIR, 'decision-table-pricing.json'), 'utf8');
    const result = importJson(original);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(JSON.parse(exportJson(result.def))).toEqual(JSON.parse(original));
  });

  it('decision-table-with-default.json: import → export round-trips the DT + downstream DECISION pair', () => {
    const original = readFileSync(join(VALID_DIR, 'decision-table-with-default.json'), 'utf8');
    const result = importJson(original);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(JSON.parse(exportJson(result.def))).toEqual(JSON.parse(original));
  });

  it('decision-table-empty-rules.json: schema parse fails with a readable error', () => {
    const original = readFileSync(join(INVALID_DIR, 'decision-table-empty-rules.json'), 'utf8');
    const result = importJson(original);
    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.errors.join('\n')).toMatch(/rules/i);
  });

  it('decision-table-bad-ref.json: schema parse succeeds but validate() emits REF_RESOLVES on nextStep', () => {
    const original = readFileSync(join(INVALID_DIR, 'decision-table-bad-ref.json'), 'utf8');
    const result = importJson(original);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    const diagnostics = validate(result.def);
    const refResolves = diagnostics.find(
      (d) => d.code === 'REF_RESOLVES' && d.field === 'nextStep' && d.nodeId === 'dt',
    );
    expect(refResolves).toBeTruthy();
  });

  it('preserves unknown sub-fields on a DECISION_TABLE step and on individual rules', () => {
    const def = {
      id: 'wf',
      name: 'wf',
      steps: [
        {
          id: 'dt',
          name: 'DT',
          type: 'DECISION_TABLE',
          hitPolicy: 'F',
          nextStep: 'e',
          decisionTable: {
            rules: [{ when: {}, outputs: { tier: 'GOLD' }, futureRuleField: 'preserved' }],
            futureDtField: { nested: true },
          },
          futureStepField: 'also-preserved',
        },
        { id: 'e', name: 'End', type: 'END' },
      ],
    };
    const result = importJson(JSON.stringify(def));
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    const exported = JSON.parse(exportJson(result.def));
    expect(exported.steps[0].decisionTable.rules[0].futureRuleField).toBe('preserved');
    expect(exported.steps[0].decisionTable.futureDtField).toEqual({ nested: true });
    expect(exported.steps[0].futureStepField).toBe('also-preserved');
  });
});
