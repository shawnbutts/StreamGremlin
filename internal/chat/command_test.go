package chat

import (
	"reflect"
	"testing"
)

func TestParseCommand(t *testing.T) {
	cases := map[string]*Command{
		"!hello":          {Name: "hello", ArgList: []string{}},
		"  !SO @Friend  ": {Name: "so", Args: "@Friend", ArgList: []string{"@Friend"}},
		"!roll 2  d20":    {Name: "roll", Args: "2  d20", ArgList: []string{"2", "d20"}},
		"!a_b9":           {Name: "a_b9", ArgList: []string{}},
		"hello !there":    nil,
		"!":               nil,
		"! hello":         nil,
		"!he-llo":         nil,
		"!ünïcode":        nil,
		"!" + string(make([]byte, 0)) + "abcdefghijabcdefghijabcdefghijabc": nil, // 33 characters
	}
	for in, want := range cases {
		got, ok := ParseCommand(in)
		if want == nil {
			if ok {
				t.Errorf("%q parsed as %+v", in, got)
			}
			continue
		}
		if len(got.ArgList) == 0 {
			got.ArgList = []string{}
		}
		if !ok || !reflect.DeepEqual(got, *want) {
			t.Errorf("%q: %+v, want %+v", in, got, *want)
		}
	}
}
