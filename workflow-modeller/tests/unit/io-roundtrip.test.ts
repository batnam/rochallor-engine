import { readFileSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { exportJson } from '@/io/export';
import { importJson } from '@/io/import';
import { describe, expect, it } from 'vitest';

const VALID_DIR = resolve(__dirname, '../fixtures/valid');

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
