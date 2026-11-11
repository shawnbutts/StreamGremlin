package lua

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"strings"

	glua "github.com/yuin/gopher-lua"
)

// Interpreter limits inside the worker.
const (
	callStackSize   = 200
	registrySize    = 1024 * 20
	registryMaxSize = 256 * 1024
	maxRepBytes     = 1 << 20
	maxFormatWidth  = 99
)

// removedBase are base functions a script never gets.
var removedBase = []string{
	"dofile", "loadfile", "load", "loadstring", "require", "module",
	"collectgarbage", "getfenv", "setfenv", "newproxy", "_printregs", "_GOPHER_LUA_VERSION",
}

// newState returns a fresh Lua state with only the sandbox's libraries
// (docs/adr/0037-lua-scripts.md). logf receives print and sg.log lines.
func newState(logf func(string)) *glua.LState {
	L := glua.NewState(glua.Options{
		SkipOpenLibs:        true,
		CallStackSize:       callStackSize,
		RegistrySize:        registrySize,
		RegistryMaxSize:     registryMaxSize,
		MinimizeStackMemory: true,
	})
	for _, lib := range []struct {
		name string
		open glua.LGFunction
	}{
		{glua.BaseLibName, glua.OpenBase},
		{glua.TabLibName, glua.OpenTable},
		{glua.StringLibName, glua.OpenString},
		{glua.MathLibName, glua.OpenMath},
		{glua.OsLibName, glua.OpenOs},
	} {
		L.Push(L.NewFunction(lib.open))
		L.Push(glua.LString(lib.name))
		L.Call(1, 0)
	}
	g := L.G.Global
	for _, name := range removedBase {
		g.RawSetString(name, glua.LNil)
	}
	// os: only the clock and dates.
	if osLib, ok := g.RawGetString("os").(*glua.LTable); ok {
		keep := L.NewTable()
		for _, name := range []string{"time", "date", "clock"} {
			keep.RawSetString(name, osLib.RawGetString(name))
		}
		g.RawSetString("os", keep)
	}
	// package.loaded would still reach the full os table.
	g.RawSetString("package", glua.LNil)
	if s, ok := g.RawGetString("string").(*glua.LTable); ok {
		s.RawSetString("dump", glua.LNil)
		s.RawSetString("rep", L.NewFunction(strRep))
		s.RawSetString("format", L.NewFunction(strFormat))
	}
	if m, ok := g.RawGetString("math").(*glua.LTable); ok {
		m.RawSetString("random", L.NewFunction(mathRandom))
		m.RawSetString("randomseed", L.NewFunction(func(*glua.LState) int { return 0 }))
	}
	logFn := L.NewFunction(func(L *glua.LState) int {
		parts := make([]string, L.GetTop())
		for i := range parts {
			parts[i] = L.ToStringMeta(L.Get(i + 1)).String()
		}
		logf(strings.Join(parts, "\t"))
		return 0
	})
	g.RawSetString("print", logFn)
	sg := L.NewTable()
	sg.RawSetString("log", logFn)
	g.RawSetString("sg", sg)
	return L
}

// strRep is string.rep with its result capped at 1 MiB.
func strRep(L *glua.LState) int {
	s := L.CheckString(1)
	n := L.CheckInt(2)
	if n <= 0 || s == "" {
		L.Push(glua.LString(""))
		return 1
	}
	if len(s) > maxRepBytes/n {
		L.RaiseError("string.rep: the result would be over 1 MiB")
	}
	L.Push(glua.LString(strings.Repeat(s, n)))
	return 1
}

// mathRandom is math.random from crypto/rand: math.random() is in [0, 1),
// math.random(m) in 1..m, math.random(m, n) in m..n.
func mathRandom(L *glua.LState) int {
	switch L.GetTop() {
	case 0:
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			L.RaiseError("math.random: no random numbers: %v", err)
		}
		L.Push(glua.LNumber(float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53)))
		return 1
	case 1:
		L.Push(glua.LNumber(randRange(L, 1, L.CheckInt64(1))))
		return 1
	default:
		L.Push(glua.LNumber(randRange(L, L.CheckInt64(1), L.CheckInt64(2))))
		return 1
	}
}

func randRange(L *glua.LState, lo, hi int64) float64 {
	if lo > hi {
		L.RaiseError("math.random: the interval is empty")
	}
	span := new(big.Int).Sub(big.NewInt(hi), big.NewInt(lo))
	span.Add(span, big.NewInt(1))
	n, err := rand.Int(rand.Reader, span)
	if err != nil {
		L.RaiseError("math.random: no random numbers: %v", err)
	}
	return float64(new(big.Int).Add(n, big.NewInt(lo)).Int64())
}

// strFormat is string.format as Lua 5.1 has it: %d %i %u %c %x %X %o %e %E
// %f %g %G %q %s %%, flags "-+ #0", width and precision at most 99.
// gopher-lua's own hands the format to Go's fmt, verbs and widths included.
func strFormat(L *glua.LState) int {
	format := L.CheckString(1)
	arg := 1
	take := func() int {
		arg++
		if arg > L.GetTop() {
			L.ArgError(arg, "no value")
		}
		return arg
	}
	var out strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			out.WriteByte(c)
			continue
		}
		i++
		if i >= len(format) {
			L.RaiseError("invalid option '%%' to 'format'")
		}
		if format[i] == '%' {
			out.WriteByte('%')
			continue
		}
		start := i
		for i < len(format) && strings.IndexByte("-+ #0", format[i]) >= 0 {
			i++
		}
		digits := func() {
			n := 0
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
				n++
			}
			if n > 2 {
				L.RaiseError("invalid format (width or precision too long)")
			}
		}
		digits()
		if i < len(format) && format[i] == '.' {
			i++
			digits()
		}
		if i >= len(format) {
			L.RaiseError("invalid option '%%' to 'format'")
		}
		spec := "%" + format[start:i]
		switch verb := format[i]; verb {
		case 'd', 'i':
			fmt.Fprintf(&out, spec+"d", int64(L.CheckNumber(take())))
		case 'u':
			fmt.Fprintf(&out, spec+"d", uint64(int64(L.CheckNumber(take())))) //nolint:gosec // Lua 5.1's %u: a negative number wraps, as in C
		case 'c':
			out.WriteByte(byte(int64(L.CheckNumber(take())))) //nolint:gosec // Lua 5.1's %c: the low byte, as in C
		case 'x', 'X', 'o':
			fmt.Fprintf(&out, spec+string(verb), int64(L.CheckNumber(take())))
		case 'e', 'E', 'f', 'g', 'G':
			fmt.Fprintf(&out, spec+string(verb), float64(L.CheckNumber(take())))
		case 'q':
			out.WriteString(quote(L.CheckString(take())))
		case 's':
			fmt.Fprintf(&out, spec+"s", L.ToStringMeta(L.Get(take())).String())
		default:
			L.RaiseError("invalid option '%%%c' to 'format'", verb)
		}
		if out.Len() > maxRepBytes {
			L.RaiseError("string.format: the result would be over 1 MiB")
		}
	}
	L.Push(glua.LString(out.String()))
	return 1
}

// quote is %q: a string Lua reads back as the same string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\', '\n':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\000`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// finite reports whether n can be given back as JSON.
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
