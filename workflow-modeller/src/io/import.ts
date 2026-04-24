import { KNOWN_ROOT_KEYS, KNOWN_STEP_KEYS, zWorkflowDefinition } from '@/domain/schema';
import type { WorkflowDefinition } from '@/domain/types';

export interface ImportOk {
  ok: true;
  def: WorkflowDefinition;
  warnings: string[];
}

export interface ImportErr {
  ok: false;
  errors: string[];
}

export type ImportResult = ImportOk | ImportErr;

export function importJson(text: string): ImportResult {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (e) {
    return { ok: false, errors: [`Invalid JSON: ${(e as Error).message}`] };
  }

  const parsed = zWorkflowDefinition.safeParse(raw);
  if (!parsed.success) {
    return {
      ok: false,
      errors: parsed.error.issues.map((i) => `${i.path.join('.') || '<root>'}: ${i.message}`),
    };
  }

  const warnings = collectUnknownFieldWarnings(parsed.data);
  return { ok: true, def: parsed.data, warnings };
}

function collectUnknownFieldWarnings(def: WorkflowDefinition): string[] {
  const warnings: string[] = [];
  const root = def as Record<string, unknown>;

  const rootExtras = Object.keys(root).filter(
    (k) => !(KNOWN_ROOT_KEYS as readonly string[]).includes(k),
  );
  if (rootExtras.length > 0) {
    warnings.push(`Unknown top-level field(s): ${rootExtras.join(', ')}`);
  }

  for (const step of def.steps) {
    const known = KNOWN_STEP_KEYS[step.type] ?? [];
    const extras = Object.keys(step).filter((k) => !known.includes(k));
    if (extras.length > 0) {
      warnings.push(`Step "${step.id}" has unknown field(s): ${extras.join(', ')}`);
    }
  }

  return warnings;
}
