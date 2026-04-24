import * as generated from './parser.generated';

interface PeggyLocation {
  start: { line: number; column: number; offset: number };
  end: { line: number; column: number; offset: number };
}

interface PeggyErrorShape extends Error {
  expected?: unknown;
  found?: unknown;
  location?: PeggyLocation;
}

const peggyParse = generated.parse as (input: string, options?: unknown) => unknown;
const PeggySyntaxError = generated.SyntaxError as unknown as new (
  ...args: unknown[]
) => PeggyErrorShape;

export type BinaryOp = '==' | '!=' | '>' | '>=' | '<' | '<=' | '+' | '-' | '*' | '/' | '&&' | '||';

export type Expr =
  | { kind: 'literal'; valueKind: 'int' | 'float'; value: number }
  | { kind: 'literal'; valueKind: 'bool'; value: boolean }
  | { kind: 'literal'; valueKind: 'string'; value: string }
  | { kind: 'identifier'; path: string[] }
  | { kind: 'unary'; op: '!'; arg: Expr }
  | { kind: 'binary'; op: BinaryOp; lhs: Expr; rhs: Expr }
  | { kind: 'membership'; needle: Expr; haystack: Expr }
  | { kind: 'call'; fn: 'contains' | 'len'; args: Expr[] };

export interface ParseOk {
  ok: true;
  ast: Expr;
}

export interface ParseErr {
  ok: false;
  message: string;
  line?: number;
  column?: number;
  offset?: number;
}

export type ParseResult = ParseOk | ParseErr;

export function parseExpression(source: string): ParseResult {
  try {
    const ast = peggyParse(source) as Expr;
    return { ok: true, ast };
  } catch (e) {
    if (e instanceof PeggySyntaxError) {
      const loc = e.location;
      return {
        ok: false,
        message: e.message,
        line: loc?.start.line,
        column: loc?.start.column,
        offset: loc?.start.offset,
      };
    }
    return { ok: false, message: (e as Error).message ?? String(e) };
  }
}

/**
 * Strip a `${…}` envelope from a string if present, otherwise return null.
 * Used for TRANSFORMATION values where `"${expr}"` marks the whole string as
 * an expression; any other shape is a literal JSON value.
 */
export function unwrapTransformationExpression(source: string): string | null {
  const trimmed = source.trim();
  if (trimmed.length < 3) return null;
  if (trimmed.startsWith('${') && trimmed.endsWith('}')) {
    return trimmed.slice(2, -1);
  }
  return null;
}

/** Walk the AST, collecting every identifier path (as dotted strings). */
export function collectIdentifiers(ast: Expr): string[] {
  const out: string[] = [];
  const walk = (node: Expr): void => {
    switch (node.kind) {
      case 'identifier':
        out.push(node.path.join('.'));
        return;
      case 'literal':
        return;
      case 'unary':
        walk(node.arg);
        return;
      case 'binary':
        walk(node.lhs);
        walk(node.rhs);
        return;
      case 'membership':
        walk(node.needle);
        walk(node.haystack);
        return;
      case 'call':
        for (const arg of node.args) walk(arg);
        return;
    }
  };
  walk(ast);
  return out;
}

/**
 * Heuristic: does the top-level AST node produce a boolean result?
 * True for explicit boolean literals, comparisons, boolean operators, `!`,
 * membership, and (optimistically) identifiers / calls (we can't infer those).
 */
export function topLevelLikelyBoolean(ast: Expr): boolean {
  switch (ast.kind) {
    case 'literal':
      return ast.valueKind === 'bool';
    case 'unary':
      return ast.op === '!';
    case 'binary':
      return (
        ast.op === '==' ||
        ast.op === '!=' ||
        ast.op === '>' ||
        ast.op === '>=' ||
        ast.op === '<' ||
        ast.op === '<=' ||
        ast.op === '&&' ||
        ast.op === '||'
      );
    case 'membership':
      return true;
    case 'identifier':
    case 'call':
      return true;
  }
}
