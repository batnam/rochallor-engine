/**
 * Drift-guard helpers (T068).
 *
 * SC-002 requires that every workflow accepted by the editor be accepted by
 * the engine. This module pairs the TypeScript validator with the Go
 * `definition.Validate` (invoked through `workflow-engine/cmd/validate-fixture`)
 * so the drift can be detected mechanically.
 *
 * Usage from Vitest: see tests/drift.test.ts.
 */
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readdirSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { zWorkflowDefinition } from '@/domain/schema';
import type { Diagnostic, WorkflowDefinition } from '@/domain/types';
import { validate } from '@/domain/validate';

const __dirname = dirname(fileURLToPath(import.meta.url));

const MODELLER_ROOT = resolve(__dirname, '..');
const ENGINE_ROOT = resolve(MODELLER_ROOT, '..', 'workflow-engine');
const FIXTURE_ROOT = join(MODELLER_ROOT, 'tests', 'fixtures');

export interface GoVerdict {
  accepted: boolean;
  error?: string;
}

export interface FixtureDriftResult {
  fixture: string;
  ts: { accepted: boolean; diagnostics: Diagnostic[] };
  go: GoVerdict;
  /** True iff TS accepts but Go rejects — the SC-002 drift case. */
  drift: boolean;
}

export function isGoAvailable(): boolean {
  const probe = spawnSync('go', ['version']);
  return probe.status === 0;
}

/**
 * Compile the Go drift runner once and return the binary path.
 * Cached for the lifetime of the Node process.
 */
let cachedBinary: string | null = null;
export function compileGoRunner(): string {
  if (cachedBinary !== null) return cachedBinary;
  const outDir = mkdtempSync(join(tmpdir(), 'wm-drift-'));
  const out = join(outDir, 'validate-fixture');
  const result = spawnSync(
    'go',
    ['build', '-o', out, './cmd/validate-fixture'],
    { cwd: ENGINE_ROOT, encoding: 'utf-8' },
  );
  if (result.status !== 0) {
    throw new Error(`go build failed: ${result.stderr || result.stdout}`);
  }
  cachedBinary = out;
  return out;
}

export function runGoValidator(fixturePath: string): GoVerdict {
  const binary = compileGoRunner();
  const stdout = execFileSync(binary, [fixturePath], { encoding: 'utf-8' });
  const parsed = JSON.parse(stdout) as GoVerdict;
  return parsed;
}

export function runTsValidator(fixturePath: string): {
  accepted: boolean;
  diagnostics: Diagnostic[];
} {
  const raw = readFileSync(fixturePath, 'utf-8');
  const json = JSON.parse(raw);
  const parsed = zWorkflowDefinition.safeParse(json);
  if (!parsed.success) {
    return {
      accepted: false,
      diagnostics: parsed.error.issues.map((i) => ({
        code: 'STEP_TYPE_VALID', // closest enum match for "schema parse failed"
        severity: 'error',
        message: `${i.path.join('.') || '<root>'}: ${i.message}`,
      })),
    };
  }
  const def: WorkflowDefinition = parsed.data;
  const diagnostics = validate(def);
  const errorCount = diagnostics.filter((d) => d.severity === 'error').length;
  return { accepted: errorCount === 0, diagnostics };
}

export function checkFixture(fixturePath: string): FixtureDriftResult {
  const ts = runTsValidator(fixturePath);
  const go = runGoValidator(fixturePath);
  return {
    fixture: fixturePath,
    ts,
    go,
    drift: ts.accepted && !go.accepted,
  };
}

export function listFixtures(subdir: 'valid' | 'invalid'): string[] {
  const dir = join(FIXTURE_ROOT, subdir);
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .filter((f) => f.endsWith('.json'))
    .sort()
    .map((f) => join(dir, f));
}
