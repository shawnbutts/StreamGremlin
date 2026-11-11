package lua

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	glua "github.com/yuin/gopher-lua"
)

// toLua copies a JSON value (as encoding/json decodes it) into L.
func toLua(L *glua.LState, v any) glua.LValue {
	switch x := v.(type) {
	case nil:
		return glua.LNil
	case bool:
		return glua.LBool(x)
	case float64:
		return glua.LNumber(x)
	case int64:
		return glua.LNumber(x)
	case int:
		return glua.LNumber(x)
	case json.Number:
		f, _ := x.Float64()
		return glua.LNumber(f)
	case string:
		return glua.LString(x)
	case []any:
		t := L.CreateTable(len(x), 0)
		for _, e := range x {
			t.Append(toLua(L, e))
		}
		return t
	case map[string]any:
		t := L.CreateTable(0, len(x))
		for k, e := range x {
			t.RawSetString(k, toLua(L, e))
		}
		return t
	}
	return glua.LString(fmt.Sprint(v))
}

// errOutput is what the script returned that can't be given back.
var errOutput = errors.New("output")

type fromLua struct {
	count int
}

// fromLua copies what a script returned into a JSON value, refusing Lua-only
// values, numbers that aren't finite and anything past the limits.
func (c *fromLua) value(v glua.LValue, depth int) (any, error) {
	c.count++
	if c.count > MaxResultCount {
		return nil, fmt.Errorf("%w: more than %d values", errOutput, MaxResultCount)
	}
	if depth > MaxResultDepth {
		return nil, fmt.Errorf("%w: tables nested more than %d deep (or a table inside itself)", errOutput, MaxResultDepth)
	}
	switch x := v.(type) {
	case *glua.LNilType:
		return nil, nil
	case glua.LBool:
		return bool(x), nil
	case glua.LNumber:
		f := float64(x)
		if !finite(f) {
			return nil, fmt.Errorf("%w: a number that isn't finite", errOutput)
		}
		if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
			return int64(f), nil
		}
		return f, nil
	case glua.LString:
		return string(x), nil
	case *glua.LTable:
		return c.table(x, depth)
	}
	return nil, fmt.Errorf("%w: a %s can't be given back", errOutput, v.Type().String())
}

func (c *fromLua) table(t *glua.LTable, depth int) (any, error) {
	n := t.Len()
	keys := 0
	var bad error
	t.ForEach(func(k, _ glua.LValue) {
		keys++
		switch k.(type) {
		case glua.LString, glua.LNumber:
		default:
			bad = fmt.Errorf("%w: a table key that's a %s (use text or numbers)", errOutput, k.Type().String())
		}
	})
	if bad != nil {
		return nil, bad
	}
	if n > 0 && keys == n {
		list := make([]any, 0, n)
		for i := 1; i <= n; i++ {
			e, err := c.value(t.RawGetInt(i), depth+1)
			if err != nil {
				return nil, err
			}
			list = append(list, e)
		}
		return list, nil
	}
	m := make(map[string]any, keys)
	var err error
	t.ForEach(func(k, e glua.LValue) {
		if err != nil {
			return
		}
		var key string
		switch kk := k.(type) {
		case glua.LString:
			key = string(kk)
		case glua.LNumber:
			key = strconv.FormatFloat(float64(kk), 'g', -1, 64)
		}
		var ev any
		ev, err = c.value(e, depth+1)
		m[key] = ev
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}
