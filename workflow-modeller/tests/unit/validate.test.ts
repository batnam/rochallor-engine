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
          boundaryEvents: [
            // biome-ignore lint/suspicious/noExplicitAny: deliberately fabricates an invalid type to exercise BOUNDARY_TYPE
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
          boundaryEvents: [{ type: 'TIMER', duration: '', interrupting: true, targetStepId: 'e' }],
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

describe('validate — DECISION_TABLE rules (007 shape)', () => {
  function dtDef(
    decisionTable: {
      rules: Array<{
        when?: Record<string, string>;
        outputs?: Record<string, unknown>;
      }>;
    },
    overrides: {
      hitPolicy?: string;
      nextStep?: string;
      extra?: Record<string, unknown>;
      tableExtra?: Record<string, unknown>;
      ruleExtras?: Record<number, Record<string, unknown>>;
    } = {},
  ): WorkflowDefinition {
    const hitPolicy = overrides.hitPolicy ?? 'F';
    const nextStep = overrides.nextStep ?? 'a';
    const rules = decisionTable.rules.map((r, i) => ({
      when: r.when ?? {},
      outputs: r.outputs ?? {},
      ...(overrides.ruleExtras?.[i] ?? {}),
    }));
    return {
      id: 'wf',
      name: 'wf',
      steps: [
        {
          id: 'dt',
          name: 'Decision Table',
          type: 'DECISION_TABLE',
          hitPolicy,
          nextStep,
          decisionTable: {
            rules,
            ...(overrides.tableExtra ?? {}),
          },
          ...(overrides.extra ?? {}),
        },
        { id: 'a', name: 'A', type: 'END' },
      ],
    } as unknown as WorkflowDefinition;
  }

  it('DECISION_TABLE_HAS_RULES fires on zero rules', () => {
    const def = dtDef({ rules: [] });
    const diags = validate(def);
    const d = diags.find((x) => x.code === 'DECISION_TABLE_HAS_RULES');
    expect(d).toBeTruthy();
    expect(d?.severity).toBe('error');
    expect(d?.nodeId).toBe('dt');
  });

  it('DECISION_TABLE_HAS_NEXT fires when nextStep is empty', () => {
    const def = dtDef({ rules: [{ outputs: { tier: 'GOLD' } }] }, { nextStep: '' });
    const d = validate(def).find((x) => x.code === 'DECISION_TABLE_HAS_NEXT');
    expect(d).toBeTruthy();
    expect(d?.severity).toBe('error');
  });

  it('REF_RESOLVES fires when nextStep points to a missing step', () => {
    const def = dtDef({ rules: [{ outputs: { tier: 'GOLD' } }] }, { nextStep: 'ghost' });
    const m = validate(def).find((d) => d.code === 'REF_RESOLVES' && d.field === 'nextStep');
    expect(m).toBeTruthy();
  });

  it('DECISION_TABLE_HIT_POLICY_UNKNOWN fires for unrecognised policy', () => {
    const def = dtDef({ rules: [{ outputs: { tier: 'GOLD' } }] }, { hitPolicy: 'X' });
    const m = validate(def).find((d) => d.code === 'DECISION_TABLE_HIT_POLICY_UNKNOWN');
    expect(m).toBeTruthy();
  });

  it('accepts each canonical hit policy value', () => {
    for (const hp of ['U', 'F', 'A', 'R', 'C', 'C+', 'C#', 'C>', 'C<']) {
      const def = dtDef({ rules: [{ outputs: { tier: 'GOLD' } }] }, { hitPolicy: hp });
      const m = validate(def).find(
        (d) =>
          d.code === 'DECISION_TABLE_HIT_POLICY_UNKNOWN' ||
          d.code === 'DECISION_TABLE_AGGREGATOR_ON_NON_C',
      );
      expect(m, `hitPolicy=${hp}`).toBeUndefined();
    }
  });

  it('DECISION_TABLE_LEGACY_THEN fires when a rule carries a legacy "then" field', () => {
    const def = dtDef(
      { rules: [{ outputs: { tier: 'GOLD' } }] },
      { ruleExtras: { 0: { then: 'a' } } },
    );
    const m = validate(def).find((d) => d.code === 'DECISION_TABLE_LEGACY_THEN');
    expect(m).toBeTruthy();
    expect(m?.message).toMatch(/Migration from 005|migration/);
  });

  it('DECISION_TABLE_LEGACY_DEFAULT_NEXT_STEP fires when the table carries a legacy defaultNextStep', () => {
    const def = dtDef(
      { rules: [{ outputs: { tier: 'GOLD' } }] },
      { tableExtra: { defaultNextStep: 'a' } },
    );
    const m = validate(def).find((d) => d.code === 'DECISION_TABLE_LEGACY_DEFAULT_NEXT_STEP');
    expect(m).toBeTruthy();
    expect(m?.message).toMatch(/Migration from 005|migration/);
  });

  it('DECISION_TABLE_UNREACHABLE_RULE fires for rules after a catch-all', () => {
    const def = dtDef({
      rules: [
        { when: { x: 'value > 0' }, outputs: { tier: 'A' } },
        { when: {}, outputs: { tier: 'B' } }, // catch-all
        { when: { x: 'value < 0' }, outputs: { tier: 'C' } }, // unreachable
        { when: {}, outputs: { tier: 'D' } }, // unreachable
      ],
    });
    const matches = validate(def).filter((d) => d.code === 'DECISION_TABLE_UNREACHABLE_RULE');
    expect(matches).toHaveLength(2);
    expect(matches.map((d) => d.ruleIndex)).toEqual([2, 3]);
    expect(matches[0]?.severity).toBe('warning');
  });

  it('STEP_FIELD_INVALID fires for each forbidden cross-type field', () => {
    const def = dtDef(
      { rules: [{ outputs: { tier: 'GOLD' } }] },
      { extra: { jobType: 'x', conditionalNextSteps: {} } },
    );
    const matches = validate(def).filter((d) => d.code === 'STEP_FIELD_INVALID');
    const fields = matches.map((d) => d.field).sort();
    expect(fields).toEqual(['conditionalNextSteps', 'jobType']);
  });

  it('DT_CELL_EXPR_SYNTAX fires on an invalid cell expression, with ruleIndex + cellColumn', () => {
    const def = dtDef({
      rules: [{ when: { score: 'value >=' }, outputs: { tier: 'GOLD' } }],
    });
    const m = validate(def).find((d) => d.code === 'DT_CELL_EXPR_SYNTAX');
    expect(m).toBeTruthy();
    expect(m?.ruleIndex).toBe(0);
    expect(m?.cellColumn).toBe('score');
  });

  it('DT_OUTPUT_EXPR_SYNTAX fires on a bad expression inside a closed ${...} template', () => {
    const def = dtDef({
      rules: [{ when: {}, outputs: { tier: '${value +}' } }],
    });
    const m = validate(def).find((d) => d.code === 'DT_OUTPUT_EXPR_SYNTAX');
    expect(m).toBeTruthy();
    expect(m?.ruleIndex).toBe(0);
    expect(m?.branchKey).toBe('tier');
  });

  it('ALL_REACHABLE walks the DT single outbound edge to nextStep', () => {
    const def = dtDef({ rules: [{ outputs: { tier: 'GOLD' } }] }, { nextStep: 'a' });
    const codes = codesIn(def);
    expect(codes).not.toContain('ALL_REACHABLE');
  });
});
