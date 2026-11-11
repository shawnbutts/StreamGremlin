package templates_test

import (
	"errors"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/templates"
)

const addOn = `{"template":{"id":"roll","title":"!roll","description":"Rolls.","blanks":[{"path":"$.steps[0].with.message","hint":"What to say"}]},
"workflow":{"schema":"streamgremlin.workflow/v1","id":"chat.roll","name":"Roll","triggers":[{"type":"chat.command.received"}],
"steps":[{"action":"chat.send","with":{"message":""}}]}}`

// An add-on's template file is read strictly (D-071).
func TestAddOnTemplates(t *testing.T) {
	tp, err := templates.ParseAddOn([]byte(addOn))
	if err != nil || tp.ID != "roll" || len(tp.Blanks) != 1 {
		t.Fatalf("%+v %v", tp, err)
	}
	for _, c := range []struct{ from, to, want string }{
		{`"id":"roll"`, `"id":"Roll"`, "lowercase"},
		{`"title":"!roll"`, `"title":""`, "title"},
		{`"blanks"`, `"category":"Chat commands","blanks"`, "no category"},
		{`"blanks"`, `"starter":true,"blanks"`, "isn't a starter"},
		{`"blanks"`, `"add_on":{"id":"x"},"blanks"`, "StreamGremlin's to say"},
		{`"blanks"`, `"extra":1,"blanks"`, "unknown field"},
		{`"hint":"What to say"`, `"hint":""`, "a hint"},
		{`"path":"$.steps[0].with.message"`, `"path":"$.steps[3].with.message"`, "isn't an empty text"},
		{`"message":""`, `"message":"hi"`, "isn't an empty text"},
		{`"workflow":{`, `"workflow":[{`, "a JSON object"},
	} {
		raw := strings.Replace(addOn, c.from, c.to, 1)
		if c.from == `"workflow":{` {
			raw = strings.TrimSuffix(raw, "}") + "]}"
		}
		if _, err := templates.ParseAddOn([]byte(raw)); !errors.Is(err, templates.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.to, err, c.want)
		}
	}
}
