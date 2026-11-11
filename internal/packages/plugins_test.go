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
	if _, err := s.InstallWith(ctx, data, Approval{Digest: pv.Digest, Permissions: []string{"chat.send"}}, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Approved(ctx, "dice"); strings.Join(got, ",") != "chat.send" {
		t.Errorf("approved: %v", got)
	}
	p, _ := s.Get(ctx, "dice")
	if p.Plugin == nil || p.Plugin.Enabled || p.Plugin.Values["sides"] != float64(6) {
		t.Fatalf("installed: %+v", p.Plugin)
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

// version is the test plugin at another version, with its manifest changed
// by pairs of from, to.
func version(t *testing.T, v string, swaps ...string) []byte {
	t.Helper()
	m := strings.Replace(fmt.Sprintf(luaManifest, ""), `"1.0.0"`, `"`+v+`"`, 1)
	for i := 0; i+1 < len(swaps); i += 2 {
		if !strings.Contains(m, swaps[i]) {
			t.Fatalf("no %s", swaps[i])
		}
		m = strings.Replace(m, swaps[i], swaps[i+1], 1)
	}
	return zipOf(t, file{name: "streamgremlin.json", body: m}, file{name: "plugin.lua", body: "-- " + v})
}

// Updating and rolling back a plugin (D-072): it asks again only for
// permissions not approved; steps it drops and settings that don't fit are
// named; its settings are kept where they fit.
func TestPluginUpdatesAndRollsBack(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	install := func(data []byte, perms ...string) (Installed, error) {
		t.Helper()
		pv, _, err := s.Check(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		return s.InstallWith(ctx, data, Approval{Digest: pv.Digest, Permissions: perms}, now)
	}
	// 1.0.0 asks for nothing.
	v1 := version(t, "1.0.0", `"permissions":["chat.send"],`, "")
	if _, err := install(v1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSettings(ctx, "dice", map[string]any{"sides": float64(20)}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(ctx, "dice", true, now); err != nil {
		t.Fatal(err)
	}

	// 1.1.0 asks for chat.send, renames roll to throw, and allows 12 sides.
	v2 := version(t, "1.1.0", `"name":"roll"`, `"name":"throw"`, `"max":100}]`, `"max":12}]`)
	pv, _, err := s.Check(ctx, v2)
	if err != nil {
		t.Fatal(err)
	}
	ch := pv.PluginUpdate
	if !pv.Update || ch == nil || len(ch.NewPermissions) != 1 || ch.NewPermissions[0].Name != "chat.send" ||
		len(ch.RemovedSteps) != 1 || ch.RemovedSteps[0].Name != "roll" || strings.Join(ch.Reset, ",") != "Sides" {
		t.Fatalf("preview: %+v %+v", pv.Update, ch)
	}
	if _, err := install(v2); !errors.Is(err, ErrConfirmPermissions) || !strings.Contains(err.Error(), "chat.send") {
		t.Fatalf("without approving: %v", err)
	}
	got, err := install(v2, "chat.send")
	if err != nil {
		t.Fatal(err)
	}
	if got.From != "1.0.0" || got.Version != "1.1.0" || strings.Join(got.Reset, ",") != "Sides" || !got.Plugin.Enabled ||
		got.Plugin.Values["sides"] != float64(6) {
		t.Fatalf("updated: %+v %+v", got, got.Plugin)
	}
	if a, _ := s.Approved(ctx, "dice"); strings.Join(a, ",") != "chat.send" {
		t.Errorf("approved: %v", a)
	}

	// Roll back: 1.0.0 asks for nothing, so nothing to approve; throw goes.
	p, _ := s.Get(ctx, "dice")
	rb := p.Plugin.Rollback
	if p.RollbackTo != "1.0.0" || rb == nil || len(rb.NewPermissions) != 0 || len(rb.RemovedSteps) != 1 || rb.RemovedSteps[0].Name != "throw" {
		t.Fatalf("rollback: %s %+v", p.RollbackTo, rb)
	}
	back, err := s.RollbackWith(ctx, "dice", nil, now)
	if err != nil || back.From != "1.1.0" || back.Version != "1.0.0" {
		t.Fatalf("rolled back: %+v %v", back, err)
	}
	if a, _ := s.Approved(ctx, "dice"); len(a) != 0 {
		t.Errorf("approved after rolling back: %v", a)
	}

	// 1.1.0 again, kept: it asks for chat.send again.
	if _, err := install(v2); !errors.Is(err, ErrConfirmPermissions) {
		t.Fatalf("again: %v", err)
	}
	if _, err := install(v2, "chat.send"); err != nil {
		t.Fatal(err)
	}
	// 1.2.0 asks for the same: no need to approve it again.
	if _, err := install(version(t, "1.2.0")); err != nil {
		t.Fatalf("same permissions: %v", err)
	}
	// Rolling back to a version that asks for more needs it approved:
	// 1.1.0 and 1.2.0 both ask for chat.send, so make one that doesn't.
	if _, err := install(version(t, "1.3.0", `"permissions":["chat.send"],`, "")); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get(ctx, "dice")
	if p.RollbackTo != "1.2.0" || len(p.Plugin.Rollback.NewPermissions) != 1 {
		t.Fatalf("rollback to 1.2.0: %+v", p.Plugin.Rollback)
	}
	if _, err := s.RollbackWith(ctx, "dice", nil, now); !errors.Is(err, ErrConfirmPermissions) {
		t.Fatalf("rolling back without approving: %v", err)
	}
	if _, err := s.RollbackWith(ctx, "dice", []string{"chat.send"}, now); err != nil {
		t.Fatal(err)
	}

	audit, _ := s.Audit(ctx, 20)
	var lines []string
	for _, a := range audit {
		lines = append(lines, a.Action+" "+a.Version+": "+a.Detail)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"update 1.1.0: Dice 1.0.0 to 1.1.0", "permissions approved: chat.send", "steps removed: roll",
		"settings reset: Sides", "rollback 1.0.0: Dice 1.1.0 back to 1.0.0"} {
		if !strings.Contains(all, want) {
			t.Errorf("audit has no %q:\n%s", want, all)
		}
	}
}
