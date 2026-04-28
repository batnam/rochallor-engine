import { describe, expect, it } from 'vitest';

import { lintDecisionExpression, lintTransformationExpression } from '@/domain/expression/lint';
import {
  type Expr,
  collectIdentifiers,
  parseExpression,
  topLevelLikelyBoolean,
  unwrapTransformationExpression,
} from '@/domain/expression/parser';

function parseOk(source: string): Expr {
  const result = parseExpression(source);
  if (!result.ok)
    throw new Error(`expected parse of ${JSON.stringify(source)} to succeed: ${result.message}`);
  return result.ast;
}

describe('parser — variable reference styles', () => {
  const cases: Array<[string, string[]]> = [
    ['creditScore', ['creditScore']],
    ['#creditScore', ['creditScore']],
    ['${creditScore}', ['creditScore']],
    ['result.eligible', ['result', 'eligible']],
    ['${result.score}', ['result', 'score']],
    ['#user.roles', ['user', 'roles']],
  ];
  for (const [source, path] of cases) {
    it(`parses ${source} as identifier path ${JSON.stringify(path)}`, () => {
      const ast = parseOk(source);
      expect(ast.kind).toBe('identifier');
      if (ast.kind === 'identifier') expect(ast.path).toEqual(path);
    });
  }
});

describe('parser — comparison operators (return bool)', () => {
  const ops: Array<[string, Expr['kind']]> = [
    ["status == 'APPROVED'", 'binary'],
    ['retryCount != 0', 'binary'],
    ['age > 18', 'binary'],
    ['creditScore >= 700', 'binary'],
    ['riskScore < 0.5', 'binary'],
    ['amount <= 10000', 'binary'],
  ];
  for (const [source] of ops) {
    it(`parses ${source}`, () => {
      const ast = parseOk(source);
      expect(ast.kind).toBe('binary');
      expect(topLevelLikelyBoolean(ast)).toBe(true);
    });
  }
});

describe('parser — arithmetic operators (return number)', () => {
  for (const source of [
    'baseAmount + fee',
    'total - discount',
    'price * quantity',
    'totalCost / itemCount',
  ]) {
    it(`parses ${source} as a non-boolean binary op`, () => {
      const ast = parseOk(source);
      expect(ast.kind).toBe('binary');
      expect(topLevelLikelyBoolean(ast)).toBe(false);
    });
  }
});

describe('parser — boolean operators', () => {
  it('parses &&', () => {
    const ast = parseOk('valid == true && creditScore >= 600');
    expect(ast.kind).toBe('binary');
    if (ast.kind === 'binary') expect(ast.op).toBe('&&');
  });
  it('parses ||', () => {
    const ast = parseOk('bypass == true || creditScore >= 800');
    expect(ast.kind).toBe('binary');
    if (ast.kind === 'binary') expect(ast.op).toBe('||');
  });
  it('parses unary !', () => {
    const ast = parseOk('!blacklisted');
    expect(ast.kind).toBe('unary');
  });
  it('respects parens', () => {
    const ast = parseOk('(a > 1 && b < 5) || c == true');
    expect(ast.kind).toBe('binary');
    if (ast.kind === 'binary') expect(ast.op).toBe('||');
  });
});

describe('parser — membership and built-ins', () => {
  it('parses `in` as membership', () => {
    const ast = parseOk("'ADMIN' in user.roles");
    expect(ast.kind).toBe('membership');
  });
  it('desugars contains(col, elem) to membership with needle=elem, haystack=col', () => {
    const ast = parseOk("contains(user.roles, 'ADMIN')");
    expect(ast.kind).toBe('membership');
    if (ast.kind === 'membership') {
      expect(ast.needle).toMatchObject({ kind: 'literal', value: 'ADMIN' });
      expect(ast.haystack).toMatchObject({ kind: 'identifier', path: ['user', 'roles'] });
    }
  });
  it('parses len(x) as a call', () => {
    const ast = parseOk('len(attachments)');
    expect(ast.kind).toBe('call');
    if (ast.kind === 'call') expect(ast.fn).toBe('len');
  });
  it('topLevelLikelyBoolean accepts membership', () => {
    const ast = parseOk("'A' in roles");
    expect(topLevelLikelyBoolean(ast)).toBe(true);
  });
});

describe('parser — literals', () => {
  const cases: Array<[string, { kind: Expr['kind']; valueKind: string; value: unknown }]> = [
    ['700', { kind: 'literal', valueKind: 'int', value: 700 }],
    ['-1', { kind: 'literal', valueKind: 'int', value: -1 }],
    ['3.14', { kind: 'literal', valueKind: 'float', value: 3.14 }],
    ['true', { kind: 'literal', valueKind: 'bool', value: true }],
    ['false', { kind: 'literal', valueKind: 'bool', value: false }],
    ["'APPROVED'", { kind: 'literal', valueKind: 'string', value: 'APPROVED' }],
    ['"ADMIN"', { kind: 'literal', valueKind: 'string', value: 'ADMIN' }],
  ];
  for (const [source, expected] of cases) {
    it(`parses literal ${source}`, () => {
      expect(parseOk(source)).toMatchObject(expected);
    });
  }
});

describe('parser — error reporting', () => {
  it('reports unclosed string', () => {
    const r = parseExpression("status == 'APPROVED");
    expect(r.ok).toBe(false);
  });
  it('reports unbalanced parens', () => {
    const r = parseExpression('(a && b');
    expect(r.ok).toBe(false);
  });
  it('reports trailing garbage', () => {
    const r = parseExpression('a > 1 +');
    expect(r.ok).toBe(false);
  });
});

describe('unwrapTransformationExpression', () => {
  it('unwraps ${expr}', () => {
    expect(unwrapTransformationExpression('${loanAmount + 50}')).toBe('loanAmount + 50');
  });
  it('returns null for literals', () => {
    expect(unwrapTransformationExpression('"APPROVED"')).toBeNull();
    expect(unwrapTransformationExpression('42')).toBeNull();
  });
  it('returns null for short strings', () => {
    expect(unwrapTransformationExpression('x')).toBeNull();
  });
});

describe('collectIdentifiers', () => {
  it('walks through binary expressions', () => {
    const ast = parseOk('creditScore >= 700 && !blacklisted');
    expect(collectIdentifiers(ast).sort()).toEqual(['blacklisted', 'creditScore']);
  });
  it('walks through membership', () => {
    const ast = parseOk("'ADMIN' in user.roles");
    expect(collectIdentifiers(ast)).toEqual(['user.roles']);
  });
});

describe('lintDecisionExpression', () => {
  const knownVars = new Set(['creditScore', 'blacklisted']);
  it('accepts a well-formed boolean expression', () => {
    expect(lintDecisionExpression('creditScore >= 700', knownVars, {})).toEqual([]);
  });
  it('rejects non-boolean expressions with DECISION_EXPR_NON_BOOLEAN', () => {
    const diags = lintDecisionExpression('creditScore + 1', knownVars, {});
    const codes = diags.map((d) => d.code);
    expect(codes).toContain('DECISION_EXPR_NON_BOOLEAN');
  });
  it('rejects syntax errors with DECISION_EXPR_SYNTAX', () => {
    const diags = lintDecisionExpression("status == 'APPROVED", knownVars, {});
    expect(diags.some((d) => d.code === 'DECISION_EXPR_SYNTAX')).toBe(true);
  });
  it('warns on unknown identifiers when the known set is non-empty', () => {
    const diags = lintDecisionExpression('ghost == true', knownVars, {});
    expect(diags.some((d) => d.code === 'DECISION_EXPR_UNKNOWN_IDENT')).toBe(true);
  });
  it('is silent on identifier references when the known set is empty (best-effort)', () => {
    const diags = lintDecisionExpression('ghost == true', new Set<string>(), {});
    expect(diags.filter((d) => d.code === 'DECISION_EXPR_UNKNOWN_IDENT')).toEqual([]);
  });
});

describe('lintTransformationExpression', () => {
  it('is silent on literal values (no ${…} envelope)', () => {
    expect(lintTransformationExpression('42', {})).toEqual([]);
    expect(lintTransformationExpression('"APPROVED"', {})).toEqual([]);
  });
  it('accepts a valid ${expr}', () => {
    expect(lintTransformationExpression('${loanAmount + 50}', {})).toEqual([]);
  });
  it('reports TRANSFORMATION_EXPR_SYNTAX on broken expressions', () => {
    const diags = lintTransformationExpression('${1 +}', {});
    expect(diags.some((d) => d.code === 'TRANSFORMATION_EXPR_SYNTAX')).toBe(true);
  });
});
