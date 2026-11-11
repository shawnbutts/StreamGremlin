package packages

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const luaManifest = `{"format":"streamgremlin.package/v1","id":"dice","name":"Dice","version":"1.0.0","publisher":"Someone",
"kind":"lua","api":"1","lua":{"script":"plugin.lua","permissions":["chat.send"],%s
"actions":[{"name":"roll","label":"Roll","description":"Rolls.","params":[
  {"name":"dice","label":"Dice","kind":"number","default":1,"min":1,"max":10},
  {"name":"who","label":"Who","kind":"text","max":60},
  {"name":"loud","label":"Loud","kind":"switch"},
  {"name":"die","label":"Die","kind":"choice","options":["d6","d20"]}]}],
"settings":[{"key":"sides","label":"Sides","kind":"number","default":6,"min":2,"max":100}]}}`

func plugin(t *testing.T, extra string, files ...file) []byte {
	t.Helper()
	return zipOf(t, append([]file{{name: "streamgremlin.json", body: fmt.Sprintf(luaManifest, extra)}, {name: "plugin.lua", body: "sg.log('hi')"}}, files...)...)
}

// What a Lua plugin's manifest and files may be (docs/adr/0038-lua-plugins.md).
func TestLuaPluginsAreChecked(t *testing.T) {
	r, err := ReadZip(plugin(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest.Lua.TimeoutSeconds() != DefaultTimeout || len(r.Manifest.Lua.Actions[0].Params) != 4 {
		t.Fatalf("%+v", r.Manifest.Lua)
	}
	swap := func(from, to string) []byte {
		return zipOf(t, file{name: "streamgremlin.json", body: strings.Replace(fmt.Sprintf(luaManifest, ""), from, to, 1)}, file{name: "plugin.lua", body: "x"})
	}
	for _, c := range []struct {
		data []byte
		want string
	}{
		{swap(`"chat.send"`, `"points.give"`), `permission "points.give" isn't one`},
		{swap(`"chat.send"`, `"chat.send","chat.send"`), "there twice"},
		{swap(`"script":"plugin.lua"`, `"script":"plugin.js"`), "a .lua file"},
		{swap(`"script":"plugin.lua"`, `"script":"../plugin.lua"`), "a .lua file"},
		{swap(`"name":"roll"`, `"name":"Roll"`), "lowercase"},
		{swap(`"actions":[`, `"actions":[{"name":"roll","label":"R","description":"d"},`), `action "roll" is there twice`},
		{swap(`"description":"Rolls."`, `"description":""`), "a description up to 300"},
		{swap(`"kind":"switch"`, `"kind":"colour"`), "text, number, switch or choice"},
		{swap(`"max":60`, `"max":600`), "a text's max is 1 to 500"},
		{swap(`"min":1,"max":10`, `"min":10,"max":1`), "min is at most its max"},
		{swap(`"default":1,`, `"default":11,`), "its default doesn't fit"},
		{swap(`"options":["d6","d20"]`, `"options":[]`), "1 to 20 options"},
		{swap(`"key":"sides"`, `"key":"api_token"`), "never hold secrets"},
		{swap(`"actions":[{`, `"actions":[],"x":[{`), "unknown field"},
		{swap(`"kind":"lua"`, `"kind":"lua","theme":{"for":["wheel"],"entry":"a.html"}`), "no theme section"},
		{swap(`"script":"plugin.lua",`, `"script":"plugin.lua","timeout":31,`), "1 to 30 seconds"},
		{plugin(t, "", file{name: "page.html", body: "x"}), "only its script, JSON and text"},
		{zipOf(t, file{name: "streamgremlin.json", body: fmt.Sprintf(luaManifest, "")}), "isn't in the package"},
		{zipOf(t, file{name: "streamgremlin.json", body: fmt.Sprintf(luaManifest, "")}, file{name: "plugin.lua", body: "\xff\xfe"}), "isn't UTF-8"},
		{zipOf(t, file{name: "streamgremlin.json", body: fmt.Sprintf(luaManifest, "")}, file{name: "plugin.lua", body: noise(MaxScriptBytes + 1)}), "over 256 KB"},
	} {
		_, err := ReadZip(c.data)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v, want %q", err, c.want)
		}
	}
}

func TestParamValues(t *testing.T) {
	r, _ := ReadZip(plugin(t, ""))
	ps := r.Manifest.Lua.Actions[0].Params
	for _, c := range []struct {
		p    int
		v    any
		want string
	}{
		{0, float64(3), ""}, {0, int64(11), "from 1 to 10"}, {0, "3", "is a number"},
		{1, "Fan", ""}, {1, strings.Repeat("x", 61), "up to 60"},
		{2, true, ""}, {2, "yes", "on or off"},
		{3, "d20", ""}, {3, "d8", "one of d6, d20"},
	} {
		err := ps[c.p].Check(c.v)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s %v: %v, want %q", ps[c.p].Name, c.v, err, c.want)
		}
	}
}

func TestPluginStore(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	data := plugin(t, "")
	pv, _, err := s.Check(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Install(ctx, data, pv.Digest, "", now); !errors.Is(err, ErrConfirmPermissions) {
		t.Fatalf("no permissions: %v", err)
	}
	if _, _, _, err := s.InstallWith(ctx, data, Approval{Digest: pv.Digest, Permissions: []string{"chat.send"}}, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Approved(ctx, "dice"); strings.Join(got, ",") != "chat.send" {
		t.Errorf("approved: %v", got)
	}
	p, _ := s.Get(ctx, "dice")
	if p.Plugin == nil || p.Plugin.Enabled || p.Plugin.Values["sides"] != float64(6) {
		t.Fatalf("installed: %+v", p.Plugin)
	}
	// Another version is refused until updates come.
	v2 := zipOf(t, file{name: "streamgremlin.json", body: strings.Replace(fmt.Sprintf(luaManifest, ""), `"1.0.0"`, `"1.1.0"`, 1)}, file{name: "plugin.lua", body: "x"})
	if _, _, err := s.Check(ctx, v2); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "remove it first") {
		t.Errorf("update: %v", err)
	}

	// Settings, checked.
	if _, err := s.SetSettings(ctx, "dice", map[string]any{"sides": float64(200)}, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("200: %v", err)
	}
	if _, err := s.SetSettings(ctx, "dice", map[string]any{"colour": "red"}, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown: %v", err)
	}
	if v, err := s.SetSettings(ctx, "dice", map[string]any{"sides": float64(20)}, now); err != nil || v["sides"] != float64(20) {
		t.Errorf("20: %v %v", v, err)
	}

	// On, failed, off, in the audit.
	if err := s.SetEnabled(ctx, "dice", true, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, "dice", "it didn't load", now); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get(ctx, "dice")
	if p.Plugin.Enabled || p.Plugin.Error != "it didn't load" {
		t.Errorf("failed: %+v", p.Plugin)
	}
	audit, _ := s.Audit(ctx, 10)
	var actions []string
	for _, a := range audit {
		actions = append(actions, a.Action)
	}
	if strings.Join(actions, ",") != "failed,on,settings,install" {
		t.Errorf("audit: %v", actions)
	}

	// Activity: the last MaxActivity kept.
	for i := range MaxActivity + 5 {
		if err := s.AddActivity(ctx, "dice", "1.0.0", Activity{At: now, Call: "log", Outcome: "ok", Detail: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	act, _ := s.ActivityOf(ctx, "dice", MaxActivity+10)
	if len(act) != MaxActivity || act[0].Detail != fmt.Sprint(MaxActivity+4) || act[len(act)-1].Detail != "5" {
		t.Errorf("activity: %d, newest %s, oldest %s", len(act), act[0].Detail, act[len(act)-1].Detail)
	}

	// Removing takes its settings, permissions and activity with it.
	if _, err := s.Remove(ctx, "dice", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivityOf(ctx, "dice", 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("after removal: %v", err)
	}
	if got, _ := s.Approved(ctx, "dice"); len(got) != 0 {
		t.Errorf("permissions kept: %v", got)
	}
	// Activity for a plugin removed meanwhile is dropped, not an error.
	if err := s.AddActivity(ctx, "dice", "1.0.0", Activity{At: now, Call: "log", Outcome: "ok"}); err != nil {
		t.Errorf("after removal: %v", err)
	}
}

// noise is text that doesn't compress much, so it gets past the ZIP's
// packing check to the script's own limit.
func noise(n int) string {
	b := make([]byte, n)
	x := uint32(1)
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = 'a' + byte(x>>24)%26
	}
	return "--" + string(b[2:])
}
