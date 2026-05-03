package expression

import (
	"container/list"
	"fmt"
	"regexp"
	"strings"
	"sync"

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

var programCache = newLRUCache(defaultCacheSize)

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

func compileExpr(preprocessed string) (*vm.Program, error) {
	if p, ok := programCache.get(preprocessed); ok {
		return p, nil
	}
	p, err := expr.Compile(preprocessed)
	if err != nil {
		return nil, err
	}
	programCache.set(preprocessed, p)
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

	program, err := compileExpr(preprocessed)
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
