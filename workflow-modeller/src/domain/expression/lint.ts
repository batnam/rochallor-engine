import type { Diagnostic } from '../types';
import {
  collectIdentifiers,
  parseExpression,
  topLevelLikelyBoolean,
  unwrapTransformationExpression,
} from './parser';

interface LintContext {
  nodeId?: string;
  field?: string;
  branchKey?: string;
}

export function lintDecisionExpression(
  source: string,
  knownVars: Set<string>,
  ctx: LintContext,
): Diagnostic[] {
  const out: Diagnostic[] = [];
  const result = parseExpression(source);
  if (!result.ok) {
    out.push({
      code: 'DECISION_EXPR_SYNTAX',
      severity: 'error',
      message: `Parse error: ${result.message}`,
      ...ctx,
    });
    return out;
  }

  if (!topLevelLikelyBoolean(result.ast)) {
    out.push({
      code: 'DECISION_EXPR_NON_BOOLEAN',
      severity: 'error',
      message: 'Decision expression must resolve to a boolean.',
      ...ctx,
    });
  }

  for (const ref of collectIdentifiers(result.ast)) {
    const head = ref.split('.')[0];
    if (!head) continue;
    if (knownVars.size > 0 && !knownVars.has(ref) && !knownVars.has(head)) {
      out.push({
        code: 'DECISION_EXPR_UNKNOWN_IDENT',
        severity: 'warning',
        message: `Unknown identifier "${ref}".`,
        ...ctx,
      });
    }
  }

  return out;
}

export function lintTransformationExpression(source: string, ctx: LintContext): Diagnostic[] {
  const inner = unwrapTransformationExpression(source);
  if (inner === null) return [];

  const result = parseExpression(inner);
  if (!result.ok) {
    return [
      {
        code: 'TRANSFORMATION_EXPR_SYNTAX',
        severity: 'error',
        message: `Parse error: ${result.message}`,
        ...ctx,
      },
    ];
  }
  return [];
}
