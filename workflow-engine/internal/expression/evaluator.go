package expression

import (
	"container/list"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
)

const defaultCacheSize = 256

// lruCache is a thread-safe LRU cache for compiled expr programs.
type lruCache struct {
	mu    sync.Mutex
	cap   int
	items map[string]*list.Element
	order *list.List
}

type cacheEntry struct {
	key     string
	program *vm.Program
}

func newLRUCache(cap int) *lruCache {
	return &lruCache{
		cap:   cap,
		items: make(map[string]*list.Element, cap),
		order: list.New(),
	}
}

func (c *lruCache) get(key string) (*vm.Program, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry).program, true
}

func (c *lruCache) set(key string, p *vm.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		return
	}
	if c.order.Len() >= c.cap {
		back := c.order.Back()
		if back != nil {
			c.order.Remove(back)
			delete(c.items, back.Value.(*cacheEntry).key)
		}
	}
	el := c.order.PushFront(&cacheEntry{key: key, program: p})
	c.items[key] = el
}

func (c *lruCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element, c.cap)
	c.order = list.New()
}

var programCache = newLRUCache(defaultCacheSize)

// coercionEnabled gates the AST patcher that wraps numeric/boolean operator
// operands with the __coerce* builtins. Default ON.
var coercionEnabled atomic.Bool

func init() { coercionEnabled.Store(true) }

// SetCoercionEnabled toggles automatic type coercion in expression operands.
// Flushes the compiled-program cache so subsequent evaluations are recompiled
// under the new setting. Intended for engine startup configuration and tests.
func SetCoercionEnabled(b bool) {
	coercionEnabled.Store(b)
	programCache.clear()
}

// builtins are functions available in all expressions.
var builtins = map[string]any{
	// contains(collection, element) reports whether element is in collection.
	"contains": func(collection any, element any) bool {
		switch c := collection.(type) {
		case []any:
			for _, v := range c {
				if v == element {
					return true
				}
			}
		case []string:
			s, ok := element.(string)
			if !ok {
				return false
			}
			for _, v := range c {
				if v == s {
					return true
				}
			}
		}
		return false
	},
	// len returns the length of a collection or string.
	"len": func(v any) int {
		switch c := v.(type) {
		case []any:
			return len(c)
		case []string:
			return len(c)
		case map[string]any:
			return len(c)
		case string:
			return len(c)
		}
		return 0
	},
	// __coerceNumber strictly coerces a value to float64 for use as an
	// operand of arithmetic or ordering operators. Numeric values pass
	// through; numeric-shaped strings are parsed via strconv.ParseFloat;
	// everything else errors. Used by the coercion patcher.
	"__coerceNumber": coerceNumberStrict,
	// __coerceNumberSoft best-effort numeric coercion for == and !=. It
	// returns a parsed float64 if the input is a numeric-shaped string;
	// otherwise it returns the input unchanged so that string equality
	// (e.g. name == "Alice") still works.
	"__coerceNumberSoft": coerceNumberSoft,
	// __coerceBool strictly coerces a value to bool for use as an operand
	// of &&, ||, or !. Booleans pass through; case-insensitive "true" /
	// "false" strings are coerced; everything else errors.
	"__coerceBool": coerceBoolStrict,
}

const coerceValuePreviewMax = 64

func valuePreview(v any) string {
	s := fmt.Sprintf("%v", v)
	if len(s) > coerceValuePreviewMax {
		return s[:coerceValuePreviewMax] + "…"
	}
	return s
}

func coerceNumberStrict(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case float32:
		return float64(x), nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, fmt.Errorf("cannot coerce value %q to number", valuePreview(x))
		}
		return f, nil
	}
	return 0, fmt.Errorf("cannot coerce value %s (type %T) to number", valuePreview(v), v)
}

func coerceNumberSoft(v any) any {
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return v
}

func coerceBoolStrict(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		switch strings.ToLower(x) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return false, fmt.Errorf("cannot coerce value %q to bool", valuePreview(x))
	}
	return false, fmt.Errorf("cannot coerce value %s (type %T) to bool", valuePreview(v), v)
}

var (
	reDollarBrace  = regexp.MustCompile(`\$\{([^}]*)\}`)
	reHashIdent    = regexp.MustCompile(`#([a-zA-Z0-9_.:-]+)`)
	reSingleQuote  = regexp.MustCompile(`'([^']*)'`)
	// contains(col, elem) is not valid expr syntax since "contains" is a binary
	// string operator there; rewrite to the equivalent "elem in col" form.
	reContainsFn = regexp.MustCompile(`\bcontains\(([^,()]+),\s*([^()]+)\)`)
)

// preprocess converts legacy #ident and ${...} variable syntax into standard
// expr syntax, normalises single-quoted strings, and rewrites contains() calls
// to the expr-native "in" operator.
func preprocess(raw string) (string, error) {
	if i := strings.Index(raw, "${"); i >= 0 {
		if !strings.Contains(raw[i:], "}") {
			return "", fmt.Errorf("unterminated '${' in expression")
		}
	}
	s := reDollarBrace.ReplaceAllString(raw, "($1)")
	s = reHashIdent.ReplaceAllString(s, "$1")
	s = reSingleQuote.ReplaceAllString(s, `"$1"`)
	s = reContainsFn.ReplaceAllString(s, "(${2}) in (${1})")
	return strings.TrimSpace(s), nil
}

// identCollector walks an AST and records every IdentifierNode value.
type identCollector struct{ names []string }

func (v *identCollector) Visit(node *ast.Node) {
	if ident, ok := (*node).(*ast.IdentifierNode); ok {
		v.names = append(v.names, ident.Value)
	}
}

// checkUndefinedIdents returns an error if the preprocessed expression references
// any identifier not present in env. This enforces the spec rule:
// "Undefined variable referenced → Runtime error; step fails".
func checkUndefinedIdents(preprocessed string, env map[string]any) error {
	tree, err := parser.Parse(preprocessed)
	if err != nil {
		return err // compile will surface a cleaner error
	}
	v := &identCollector{}
	ast.Walk(&tree.Node, v)
	for _, name := range v.names {
		if _, ok := env[name]; !ok {
			return fmt.Errorf("undefined variable %q", name)
		}
	}
	return nil
}

// coercionPatcher rewrites the AST of comparison, arithmetic, and boolean
// operators so that string-typed variables silently behave like the type the
// operator expects.
//
// Operator → wrapper:
//   <, <=, >, >=, +, -, *, /    →  __coerceNumber (strict)
//   ==, !=                      →  __coerceNumberSoft (best-effort)
//   &&, ||                      →  __coerceBool (strict)
//   unary !                     →  __coerceBool (strict)
//
// Operands that are already typed literals (NumberNode, BoolNode, NilNode) or
// already wrapped in a coerce call are not re-wrapped.
type coercionPatcher struct{}

var coerceBuiltinNames = map[string]struct{}{
	"__coerceNumber":     {},
	"__coerceNumberSoft": {},
	"__coerceBool":       {},
}

func (coercionPatcher) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.BinaryNode:
		switch n.Operator {
		case "<", "<=", ">", ">=", "+", "-", "*", "/":
			n.Left = wrapCoerce(n.Left, "__coerceNumber")
			n.Right = wrapCoerce(n.Right, "__coerceNumber")
		case "==", "!=":
			n.Left = wrapCoerce(n.Left, "__coerceNumberSoft")
			n.Right = wrapCoerce(n.Right, "__coerceNumberSoft")
		case "&&", "||":
			n.Left = wrapCoerce(n.Left, "__coerceBool")
			n.Right = wrapCoerce(n.Right, "__coerceBool")
		}
	case *ast.UnaryNode:
		if n.Operator == "!" {
			n.Node = wrapCoerce(n.Node, "__coerceBool")
		}
	}
}

func wrapCoerce(child ast.Node, builtin string) ast.Node {
	switch c := child.(type) {
	case *ast.BoolNode, *ast.NilNode, *ast.FloatNode, *ast.IntegerNode, *ast.StringNode:
		return child
	case *ast.CallNode:
		if id, ok := c.Callee.(*ast.IdentifierNode); ok {
			if _, isCoerce := coerceBuiltinNames[id.Value]; isCoerce {
				return child
			}
		}
	}
	return &ast.CallNode{
		Callee:    &ast.IdentifierNode{Value: builtin},
		Arguments: []ast.Node{child},
	}
}

func compileExpr(preprocessed string, coerce bool) (*vm.Program, error) {
	cacheKey := preprocessed
	if coerce {
		cacheKey = "coerce:" + preprocessed
	}
	if p, ok := programCache.get(cacheKey); ok {
		return p, nil
	}
	var p *vm.Program
	var err error
	if coerce {
		p, err = expr.Compile(preprocessed, expr.Patch(coercionPatcher{}))
	} else {
		p, err = expr.Compile(preprocessed)
	}
	if err != nil {
		return nil, err
	}
	programCache.set(cacheKey, p)
	return p, nil
}

// Evaluate evaluates expression against vars and returns the result value.
// Returns an error on malformed expressions or runtime failures.
// The result may be bool, numeric, string, or any other type the expression produces.
//
// Supported syntax:
//   - Legacy variable refs: #ident, ${ident}, ${ident.field}
//   - Arithmetic: +, -, *, /
//   - Comparison: ==, !=, >=, <=, >, <
//   - Boolean: &&, ||, !
//   - Grouping: (…)
//   - Built-ins: contains(collection, element), len(collection)
func Evaluate(expression string, vars map[string]any) (any, error) {
	preprocessed, err := preprocess(expression)
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", expression, err)
	}

	program, err := compileExpr(preprocessed, coercionEnabled.Load())
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", expression, err)
	}

	env := make(map[string]any, len(vars)+len(builtins))
	for k, v := range builtins {
		env[k] = v
	}
	for k, v := range vars {
		env[k] = v
	}

	if err := checkUndefinedIdents(preprocessed, env); err != nil {
		return nil, fmt.Errorf("expression %q: %w", expression, err)
	}

	result, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", expression, err)
	}

	return result, nil
}
