{{
  function mkBin(op, lhs, rhs) { return { kind: 'binary', op: op, lhs: lhs, rhs: rhs }; }
  function mkMem(needle, haystack) { return { kind: 'membership', needle: needle, haystack: haystack }; }
  function foldBin(head, tail, opIdx, rhsIdx) {
    return tail.reduce(function (lhs, t) { return mkBin(t[opIdx], lhs, t[rhsIdx]); }, head);
  }
  function foldRel(head, tail) {
    return tail.reduce(function (lhs, t) {
      var op = t[1];
      var rhs = t[3];
      return op === 'in' ? mkMem(lhs, rhs) : mkBin(op, lhs, rhs);
    }, head);
  }
}}

Start
  = _ expr:OrExpr _ { return expr; }

OrExpr
  = head:AndExpr tail:(_ "||" _ AndExpr)* {
      return tail.reduce(function (lhs, t) { return mkBin('||', lhs, t[3]); }, head);
    }

AndExpr
  = head:EqExpr tail:(_ "&&" _ EqExpr)* {
      return tail.reduce(function (lhs, t) { return mkBin('&&', lhs, t[3]); }, head);
    }

EqExpr
  = head:RelExpr tail:(_ op:EqOp _ RelExpr)* {
      return foldBin(head, tail, 1, 3);
    }

EqOp = op:("==" / "!=") { return op; }

RelExpr
  = head:AddExpr tail:(_ op:RelOp _ AddExpr)* {
      return foldRel(head, tail);
    }

RelOp
  = ">=" { return '>='; }
  / "<=" { return '<='; }
  / ">"  { return '>'; }
  / "<"  { return '<'; }
  / "in" !IdentRest { return 'in'; }

AddExpr
  = head:MulExpr tail:(_ op:AddOp _ MulExpr)* {
      return foldBin(head, tail, 1, 3);
    }

AddOp = op:("+" / "-") { return op; }

MulExpr
  = head:UnaryExpr tail:(_ op:MulOp _ UnaryExpr)* {
      return foldBin(head, tail, 1, 3);
    }

MulOp = op:("*" / "/") { return op; }

UnaryExpr
  = "!" _ arg:UnaryExpr { return { kind: 'unary', op: '!', arg: arg }; }
  / Primary

Primary
  = Call
  / ParenExpr
  / Literal
  / IdentifierExpr

ParenExpr
  = "(" _ expr:OrExpr _ ")" { return expr; }

Call
  = fn:CallFn _ "(" _ args:Args _ ")" {
      if (fn === 'contains' && args.length === 2) {
        return mkMem(args[1], args[0]);
      }
      return { kind: 'call', fn: fn, args: args };
    }

CallFn
  = fn:("contains" / "len") !IdentRest { return fn; }

Args
  = head:OrExpr tail:(_ "," _ OrExpr)* {
      return [head].concat(tail.map(function (t) { return t[3]; }));
    }
  / _ { return []; }

Literal
  = FloatLit
  / IntLit
  / BoolLit
  / StringLit

FloatLit
  = sign:("-")? digits:$([0-9]+) "." frac:$([0-9]+) {
      var text = (sign || '') + digits + '.' + frac;
      return { kind: 'literal', valueKind: 'float', value: parseFloat(text) };
    }

IntLit
  = sign:("-")? digits:$([0-9]+) {
      var text = (sign || '') + digits;
      return { kind: 'literal', valueKind: 'int', value: parseInt(text, 10) };
    }

BoolLit
  = v:("true" / "false") !IdentRest {
      return { kind: 'literal', valueKind: 'bool', value: v === 'true' };
    }

StringLit
  = "'" chars:$([^']*) "'" { return { kind: 'literal', valueKind: 'string', value: chars }; }
  / "\"" chars:$([^\"]*) "\"" { return { kind: 'literal', valueKind: 'string', value: chars }; }

IdentifierExpr
  = DollarIdent
  / HashIdent
  / BareIdent

DollarIdent
  = "${" _ path:IdentPath _ "}" { return { kind: 'identifier', path: path }; }

HashIdent
  = "#" path:IdentPath { return { kind: 'identifier', path: path }; }

BareIdent
  = path:IdentPath { return { kind: 'identifier', path: path }; }

IdentPath
  = head:Ident tail:("." Ident)* {
      return [head].concat(tail.map(function (t) { return t[1]; }));
    }

Ident
  = first:[a-zA-Z_] rest:IdentRest* { return first + rest.join(''); }

IdentRest
  = [a-zA-Z0-9_]

_ "whitespace"
  = [ \t\n\r]*
