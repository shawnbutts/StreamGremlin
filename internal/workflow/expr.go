package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/ext"
)

// Expressions are CEL: no loops, no side effects, and a cost limit, so a
// workflow can't hang the engine. Variables: event, vars, steps, job, each
// and saved (saved.global, saved.viewer, saved.workflow).

// costLimit caps one evaluation's work.
const costLimit = 100_000

// MaxTemplateBytes caps a formatted with-value.
const MaxTemplateBytes = 64 << 10

var (
	envOnce sync.Once
	env     *cel.Env
	envErr  error
	cache   sync.Map // source -> cel.Program
)

func celEnv() (*cel.Env, error) {
	envOnce.Do(func() {
		env, envErr = cel.NewEnv(
			cel.Variable("event", cel.DynType),
			cel.Variable("vars", cel.DynType),
			cel.Variable("steps", cel.DynType),
			cel.Variable("job", cel.DynType),
			cel.Variable("each", cel.DynType),
			cel.Variable("saved", cel.DynType),
			ext.Strings(),
			cel.OptionalTypes(),
			cel.CrossTypeNumericComparisons(true),
			cel.ParserRecursionLimit(32),
			cel.ParserExpressionSizeLimit(MaxExprBytes),
		)
	})
	return env, envErr
}

// compile checks and caches an expression.
func compile(src string) (cel.Program, error) {
	if p, ok := cache.Load(src); ok {
		return p.(cel.Program), nil
	}
	e, err := celEnv()
	if err != nil {
		return nil, err
	}
	ast, iss := e.Compile(src)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("the expression %q doesn't work: %s", short(src), strings.ReplaceAll(iss.Err().Error(), "\n", " "))
	}
	p, err := e.Program(ast, cel.CostLimit(costLimit), cel.InterruptCheckFrequency(100))
	if err != nil {
		return nil, err
	}
	cache.Store(src, p)
	return p, nil
}

func short(s string) string {
	if len(s) > 60 {
		return s[:57] + "…"
	}
	return s
}

// scope is what an expression can see.
type scope struct {
	event, vars, steps, job map[string]any
	each                    map[string]any
	saved                   map[string]any // global, viewer, workflow: name -> value
}

func (s scope) activation() map[string]any {
	each := s.each
	if each == nil {
		each = map[string]any{}
	}
	saved := s.saved
	if saved == nil {
		saved = map[string]any{"global": map[string]any{}, "viewer": map[string]any{}, "workflow": map[string]any{}}
	}
	return map[string]any{"event": s.event, "vars": s.vars, "steps": s.steps, "job": s.job, "each": each, "saved": saved}
}

// eval runs an expression and returns a plain Go value: string, int64,
// float64, bool, nil, []any or map[string]any.
func eval(ctx context.Context, src string, sc scope) (any, error) {
	p, err := compile(src)
	if err != nil {
		return nil, err
	}
	out, _, err := p.ContextEval(ctx, sc.activation())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%q: %w", short(src), err)
	}
	return native(out)
}

// evalBool runs a condition. Anything but true or false is an error, not false.
func evalBool(ctx context.Context, src string, sc scope) (bool, error) {
	v, err := eval(ctx, src, sc)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("the condition %q gave %s, not true or false", short(src), describe(v))
	}
	return b, nil
}

func describe(v any) string {
	switch v.(type) {
	case nil:
		return "nothing"
	case string:
		return "text"
	case int64, float64:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		return "a map"
	}
	return fmt.Sprintf("%T", v)
}

// native converts a CEL value to plain Go.
func native(v ref.Val) (any, error) {
	switch x := v.(type) {
	case types.Null:
		return nil, nil
	case types.Bool:
		return bool(x), nil
	case types.Int:
		return int64(x), nil
	case types.Uint:
		if uint64(x) > math.MaxInt64 {
			return float64(x), nil
		}
		return int64(x), nil //nolint:gosec // checked just above
	case types.Double:
		return float64(x), nil
	case types.String:
		return string(x), nil
	case *types.Err:
		return nil, x
	}
	if l, ok := v.(traits.Lister); ok {
		n, _ := l.Size().(types.Int)
		out := make([]any, 0, int(n))
		for it := l.Iterator(); it.HasNext() == types.True; {
			e, err := native(it.Next())
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	}
	if m, ok := v.(traits.Mapper); ok {
		out := map[string]any{}
		for it := m.Iterator(); it.HasNext() == types.True; {
			k := it.Next()
			ks, ok := k.(types.String)
			if !ok {
				return nil, errors.New("a map with keys that aren't text")
			}
			e, err := native(m.Get(k))
			if err != nil {
				return nil, err
			}
			out[string(ks)] = e
		}
		return out, nil
	}
	if o, ok := v.(interface{ Value() any }); ok {
		return normalize(o.Value()), nil
	}
	return nil, fmt.Errorf("an expression gave %s, which a workflow can't use", v.Type())
}

// normalize turns decoded JSON into what expressions see: whole numbers
// become integers, so payload counts add up as integers.
func normalize(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
		return x
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case []any:
		for i := range x {
			x[i] = normalize(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = normalize(x[k])
		}
		return x
	}
	return v
}

// templateExprs finds the ${…} parts of a template. "$${" is a literal "${".
func templateExprs(s string) ([]string, error) {
	parts, err := splitTemplate(s)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range parts {
		if p.expr {
			out = append(out, p.text)
		}
	}
	return out, nil
}

type part struct {
	text string
	expr bool
}

func splitTemplate(s string) ([]part, error) {
	var parts []part
	var lit strings.Builder
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "$${"):
			lit.WriteString("${")
			i += 3
		case strings.HasPrefix(s[i:], "${"):
			end, err := exprEnd(s, i+2)
			if err != nil {
				return nil, err
			}
			if lit.Len() > 0 {
				parts = append(parts, part{text: lit.String()})
				lit.Reset()
			}
			e := strings.TrimSpace(s[i+2 : end])
			if e == "" {
				return nil, errors.New("an empty ${} in text")
			}
			parts = append(parts, part{text: e, expr: true})
			i = end + 1
		default:
			lit.WriteByte(s[i])
			i++
		}
	}
	if lit.Len() > 0 {
		parts = append(parts, part{text: lit.String()})
	}
	return parts, nil
}

// exprEnd finds the "}" closing a ${, skipping braces and quotes inside.
func exprEnd(s string, from int) (int, error) {
	depth := 0
	var quote byte
	for i := from; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i, nil
			}
			depth--
		}
	}
	return 0, errors.New("a ${ without its closing }")
}

// render evaluates a with-value. A string that is exactly "${expr}" keeps
// the expression's type; other strings are formatted.
func render(ctx context.Context, raw json.RawMessage, sc scope) (any, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return renderValue(ctx, normalize(v), sc)
}

func renderValue(ctx context.Context, v any, sc scope) (any, error) {
	switch x := v.(type) {
	case string:
		parts, err := splitTemplate(x)
		if err != nil {
			return nil, err
		}
		if len(parts) == 1 && parts[0].expr {
			return eval(ctx, parts[0].text, sc)
		}
		var b strings.Builder
		for _, p := range parts {
			if !p.expr {
				b.WriteString(p.text)
				continue
			}
			r, err := eval(ctx, p.text, sc)
			if err != nil {
				return nil, err
			}
			b.WriteString(format(r))
			if b.Len() > MaxTemplateBytes {
				return nil, errors.New("text over 64 KiB")
			}
		}
		return b.String(), nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := renderValue(ctx, e, sc)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := map[string]any{}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			r, err := renderValue(ctx, x[k], sc)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	}
	return v, nil
}

// format writes a value into text.
func format(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
