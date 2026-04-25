import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { zWorkflowDefinition } from '@/domain/schema';
import type { DiagnosticCode, WorkflowDefinition } from '@/domain/types';
import { validate } from '@/domain/validate';

const __dirname = dirname(fileURLToPath(import.meta.url));
const INVALID_DIR = join(__dirname, '..', 'fixtures', 'invalid');
const VALID_DIR = join(__dirname, '..', 'fixtures', 'valid');

function loadFixture(relativePath: string): WorkflowDefinition {
  const raw = readFileSync(join(INVALID_DIR, relativePath), 'utf-8');
  const parsed = zWorkflowDefinition.safeParse(JSON.parse(raw));
  if (!parsed.success) {
    throw new Error(
      `Fixture ${relativePath} fails Zod parse — these fixtures must be schema-valid: ${parsed.error.message}`,
    );
  }
  return parsed.data;
}

function loadValid(relativePath: string): WorkflowDefinition {
  const raw = readFileSync(join(VALID_DIR, relativePath), 'utf-8');
  return zWorkflowDefinition.parse(JSON.parse(raw));
}

function codesIn(def: WorkflowDefinition): DiagnosticCode[] {
  return validate(def).map((d) => d.code);
}

/** Rules covered by a JSON fixture whose filename encodes the target rule. */
const fixtureCases: Array<[string, DiagnosticCode]> = [
  ['id-format.json', 'ID_FORMAT'],
  ['name-required.json', 'NAME_REQUIRED'],
  ['name-required-step.json', 'NAME_REQUIRED'],
  ['steps-nonempty.json', 'STEPS_NONEMPTY'],
  ['step-id-unique.json', 'STEP_ID_UNIQUE'],
  ['step-id-required.json', 'STEP_ID_REQUIRED'],
  ['next-workflow-consistency.json', 'NEXT_WORKFLOW_CONSISTENCY'],
  ['decision-has-branches.json', 'DECISION_HAS_BRANCHES'],
  ['transformation-has-next.json', 'TRANSFORMATION_HAS_NEXT'],
  ['transformation-has-entries.json', 'TRANSFORMATION_HAS_ENTRIES'],
  ['wait-has-next.json', 'WAIT_HAS_NEXT'],
  ['parallel-min-branches.json', 'PARALLEL_MIN_BRANCHES'],
  ['parallel-has-join.json', 'PARALLEL_HAS_JOIN'],
  ['join-has-next.json', 'JOIN_HAS_NEXT'],
  ['ref-resolves.json', 'REF_RESOLVES'],
  ['all-reachable.json', 'ALL_REACHABLE'],
  ['end-reachable.json', 'END_REACHABLE'],
  ['boundary-target-resolves.json', 'BOUNDARY_TARGET_RESOLVES'],
  ['boundary-parent-supports.json', 'BOUNDARY_PARENT_SUPPORTS'],
  ['no-nested-parallel.json', 'NO_NESTED_PARALLEL'],
  ['decision-expr-syntax.json', 'DECISION_EXPR_SYNTAX'],
  ['decision-expr-non-boolean.json', 'DECISION_EXPR_NON_BOOLEAN'],
  ['transformation-expr-syntax.json', 'TRANSFORMATION_EXPR_SYNTAX'],
];

describe('validate — fixture-driven rules', () => {
  for (const [file, code] of fixtureCases) {
    it(`fixture ${file} surfaces ${code}`, () => {
      const def = loadFixture(file);
      expect(codesIn(def)).toContain(code);
    });
  }
});

describe('validate — rules that bypass Zod parsing', () => {
  it('STEP_TYPE_VALID — unknown type on a raw definition', () => {
    const def = {
      id: 'wf',
      name: 'wf',
      steps: [
        // biome-ignore lint/suspicious/noExplicitAny: intentional bypass for this test
        { id: 's1', name: 'Bogus', type: 'NOT_A_TYPE' as any },
      ],
    } as unknown as WorkflowDefinition;
    expect(codesIn(def)).toContain('STEP_TYPE_VALID');
  });

  it('BOUNDARY_TYPE — non-TIMER boundary event on a raw definition', () => {
    const def = {
      id: 'wf',
      name: 'wf',
      steps: [
        {
          id: 's',
          name: 'S',
          type: 'SERVICE_TASK',
          jobType: 'x',
          nextStep: 'e',
          // biome-ignore lint/suspicious/noExplicitAny: intentional bypass
          boundaryEvents: [
            { type: 'MESSAGE', duration: 'PT30S', interrupting: true, targetStepId: 'e' } as any,
          ],
        },
        { id: 'e', name: 'End', type: 'END' },
      ],
    } as unknown as WorkflowDefinition;
    expect(codesIn(def)).toContain('BOUNDARY_TYPE');
  });

  it('BOUNDARY_DURATION — empty duration on a raw definition', () => {
    const def = {
      id: 'wf',
      name: 'wf',
      steps: [
        {
          id: 's',
          name: 'S',
          type: 'SERVICE_TASK',
          jobType: 'x',
          nextStep: 'e',
          boundaryEvents: [
            { type: 'TIMER', duration: '', interrupting: true, targetStepId: 'e' },
          ],
        },
        { id: 'e', name: 'End', type: 'END' },
      ],
    } as unknown as WorkflowDefinition;
    expect(codesIn(def)).toContain('BOUNDARY_DURATION');
  });
});

describe('validate — warning-level rules', () => {
  it('DECISION_EXPR_UNKNOWN_IDENT fires when a branch references an identifier not produced by any transformation', () => {
    const def: WorkflowDefinition = {
      id: 'wf',
      name: 'wf',
      steps: [
        {
          id: 't',
          name: 'Seed',
          type: 'TRANSFORMATION',
          nextStep: 'd',
          transformations: { score: '${42}' },
        },
        {
          id: 'd',
          name: 'Decide',
          type: 'DECISION',
          conditionalNextSteps: { 'ghost == true': 'e' },
        },
        { id: 'e', name: 'End', type: 'END' },
      ],
    };
    const codes = codesIn(def);
    expect(codes).toContain('DECISION_EXPR_UNKNOWN_IDENT');
  });

  it('UNKNOWN_FIELDS_PRESENT fires for unknown top-level and step fields', () => {
    const def = {
      id: 'wf',
      name: 'wf',
      legacyOwner: 'eng@corp',
      steps: [
        {
          id: 'e',
          name: 'End',
          type: 'END',
          legacyNote: 'preserved',
        },
      ],
    } as unknown as WorkflowDefinition;
    const diagnostics = validate(def);
    const unknowns = diagnostics.filter((d) => d.code === 'UNKNOWN_FIELDS_PRESENT');
    expect(unknowns.length).toBeGreaterThanOrEqual(2); // root + step
    for (const d of unknowns) {
      expect(d.severity).toBe('warning');
    }
  });

  it('GRAPH_CYCLE surfaces as a warning when there is a cycle', () => {
    const def = loadFixture('end-reachable.json');
    const cycle = validate(def).find((d) => d.code === 'GRAPH_CYCLE');
    expect(cycle).toBeTruthy();
    expect(cycle?.severity).toBe('warning');
  });
});

describe('validate — valid fixtures produce zero errors', () => {
  const validFiles = ['loan-application-full.json', 'loan-disbursement-workflow.json'];
  for (const file of validFiles) {
    it(`valid/${file} has no error-level diagnostics`, () => {
      const def = loadValid(file);
      const errors = validate(def).filter((d) => d.severity === 'error');
      expect(errors).toEqual([]);
    });
  }
});
