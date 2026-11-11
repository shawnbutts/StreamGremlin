package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// themeZip is a small installed theme's ZIP.
func themeZip(t *testing.T, version string) []byte {
	return themeZipBy(t, version, "Someone", `"Cream","Sky"`)
}

func themeZipBy(t *testing.T, version, publisher, papers string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range map[string]string{
		"streamgremlin.json": `{"format":"streamgremlin.package/v1","id":"cosy-wheel","name":"Cosy wheel","version":"` + version + `",
			"publisher":"` + publisher + `","kind":"theme","api":"1","theme":{"for":["wheel"],"entry":"index.html",
			"settings":[{"key":"paper","label":"Paper","kind":"choice","default":"Cream","options":[` + papers + `]}]}}`,
		"index.html": "<!doctype html>",
	} {
		f, _ := w.Create(name)
		_, _ = f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Installed themes through the API (docs/adr/0036-packages-plugins-and-themes.md):
// checked without storing, installed only as confirmed, offered to wheel
// overlays, and removed with their overlays back to Classic.
func TestPackagesThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	call := func(method, path, body string) (int, string) {
		w := do(t, s, req{method: method, path: path, token: testToken, body: body})
		return w.Code, w.Body.String()
	}
	data := string(themeZip(t, "1.0.0"))
	code, out := call("POST", "/v1/packages/check", data)
	var pv struct {
		ID, Digest, Publisher string
		Same                  bool
	}
	_ = json.Unmarshal([]byte(out), &pv)
	if code != 200 || pv.ID != "cosy-wheel" || len(pv.Digest) != 64 || pv.Publisher != "Someone" {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/packages", ""); out != `{"packages":[]}`+"\n" {
		t.Fatal("checking stored it:", out)
	}
	if code, out := call("POST", "/v1/packages/check", "not a zip"); code != 400 || !strings.Contains(out, "isn't a ZIP") {
		t.Fatal(code, out)
	}
	for _, dg := range []string{"", "abc", strings.Repeat("0", 64)} {
		if code, out := call("POST", "/v1/packages?digest="+dg, data); code != 400 {
			t.Fatal(dg, code, out)
		}
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest, data); code != 201 || !strings.Contains(out, `"version":"1.0.0"`) {
		t.Fatal(code, out)
	}
	if code, _ := call("POST", "/v1/packages?digest="+pv.Digest, data); code != 200 {
		t.Fatal("again:", code)
	}
	// Offered to wheel overlays, its odds not checked, its settings checked.
	if _, out := call("GET", "/v1/overlays", ""); !strings.Contains(out, `"id":"cosy-wheel","name":"Cosy wheel","description":"","odds_checked":false`) {
		t.Fatal(out)
	}
	if code, out := call("POST", "/v1/overlays", `{"name":"wheel","kind":"wheel","look":{"theme":"cosy-wheel","theme_settings":{"paper":"Plaid"}}}`); code != 400 {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/overlays", `{"name":"wheel","kind":"wheel","look":{"theme":"cosy-wheel","theme_settings":{"paper":"Sky"}}}`); code != 201 {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/packages", ""); !strings.Contains(out, `"used_by":["wheel"]`) || !strings.Contains(out, `"publisher":"Someone"`) {
		t.Fatal(out)
	}
	// An update: the overlay moves with it, and its paper, which 2.0.0
	// doesn't offer, goes back to the default, named.
	v2 := string(themeZipBy(t, "2.0.0", "Someone", `"Cream","Mint"`))
	_, out = call("POST", "/v1/packages/check", v2)
	_ = json.Unmarshal([]byte(out), &pv)
	if !strings.Contains(out, `"update":true`) || !strings.Contains(out, `"installed":{`) || !strings.Contains(out, `"publisher_change":false`) {
		t.Fatal(out)
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest, v2); code != 201 || !strings.Contains(out, `"from":"1.0.0"`) ||
		!strings.Contains(out, `"overlays":[{"overlay":"wheel","reset":["Paper"]}]`) || !strings.Contains(out, `"rollback_to":"1.0.0"`) {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/overlays", ""); strings.Contains(out, `"paper":"Sky"`) || !strings.Contains(out, `"theme":"cosy-wheel"`) {
		t.Fatal(out)
	}
	// Rolled back, and nothing before that.
	if code, out := call("POST", "/v1/packages/cosy-wheel/rollback", ""); code != 200 || !strings.Contains(out, `"version":"1.0.0"`) ||
		!strings.Contains(out, `"from":"2.0.0"`) || !strings.Contains(out, `"kept":["2.0.0"]`) {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages/cosy-wheel/rollback", ""); code != 409 || !strings.Contains(out, "no version") {
		t.Fatal(code, out)
	}
	if code, _ := call("POST", "/v1/packages/nothing-here/rollback", ""); code != 404 {
		t.Fatal(code)
	}
	// Another publisher's: only with their name, exactly.
	v3 := string(themeZipBy(t, "3.0.0", "Someone Else", `"Cream","Sky"`))
	_, out = call("POST", "/v1/packages/check", v3)
	_ = json.Unmarshal([]byte(out), &pv)
	if !strings.Contains(out, `"publisher_change":true`) {
		t.Fatal(out)
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest, v3); code != 409 || !strings.Contains(out, `"confirm_publisher"`) {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest+"&publisher=Someone", v3); code != 409 {
		t.Fatal(code, out)
	}
	if code, out := call("POST", "/v1/packages?digest="+pv.Digest+"&publisher=Someone+Else", v3); code != 201 ||
		!strings.Contains(out, `"publisher":"Someone Else"`) {
		t.Fatal(code, out)
	}
	// Removed: the overlay goes back to Classic.
	if code, out := call("DELETE", "/v1/packages/cosy-wheel", ""); code != 200 || !strings.Contains(out, `"overlays":["wheel"]`) {
		t.Fatal(code, out)
	}
	if _, out := call("GET", "/v1/overlays", ""); strings.Contains(out, `"theme":"cosy-wheel"`) {
		t.Fatal(out)
	}
	if code, _ := call("DELETE", "/v1/packages/cosy-wheel", ""); code != 404 {
		t.Fatal(code)
	}
	if _, out := call("GET", "/v1/overlays", ""); strings.Contains(out, "cosy-wheel") {
		t.Fatal("still offered:", out)
	}
	code, out = call("GET", "/v1/packages/audit", "")
	if code != 200 || strings.Count(out, `"action":"install"`) != 1 || strings.Count(out, `"action":"remove"`) != 1 ||
		strings.Count(out, `"action":"update"`) != 2 || strings.Count(out, `"action":"rollback"`) != 1 {
		t.Fatal(code, out)
	}
	if code, _ := call("GET", "/v1/packages/audit?limit=0", ""); code != 400 {
		t.Fatal(code)
	}
}
