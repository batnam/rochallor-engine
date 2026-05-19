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

  // Identifier resolution: best-effort warning when the expression references
  // a root variable that no TRANSFORMATION produces. Skipped when knownVars is
  // empty (the editor can't enumerate workflow-start variables, so silence is
  // safer than spam).
  if (knownVars.size > 0) {
    const seen = new Set<string>();
    for (const path of collectIdentifiers(result.ast)) {
      const root = path.split('.', 1)[0] ?? path;
      if (knownVars.has(root) || seen.has(root)) continue;
      seen.add(root);
      out.push({
        code: 'DECISION_EXPR_UNKNOWN_IDENT',
        severity: 'warning',
        message: `Identifier "${root}" is not produced by any TRANSFORMATION step.`,
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
