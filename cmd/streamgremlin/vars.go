package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/variables"
)

const varsUsage = `usage:
  streamgremlin vars [-q TEXT] [-json] [WHERE]   list saved variables
  streamgremlin vars set WHERE NAME VALUE        save one
  streamgremlin vars delete WHERE NAME           delete one

WHERE is global, viewer:<platform>:<user id> (viewer:twitch:12345) or
workflow:<workflow id>. With list, a bare viewer or workflow lists them all.
VALUE is read as JSON (5, true, "text", [1,2]); anything else is saved as text.`

// where splits global, viewer:twitch:1 or workflow:chat.hello.
func where(s string) (variables.Scope, string) {
	scope, owner, _ := strings.Cut(s, ":")
	return variables.Scope(scope), owner
}

// parseValue reads JSON, or else takes the text as it is.
func parseValue(s string) any {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err == nil && !dec.More() && v != nil {
		return variables.Normalize(v)
	}
	return s
}

func varsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, varsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch {
	case len(args) == 4 && args[0] == "set":
		scope, owner := where(args[1])
		body := map[string]any{"scope": scope, "owner": owner, "name": args[2], "value": parseValue(args[3])}
		var out variables.Variable
		if code := apiExit(c.Do(ctx, "PUT", "/v1/variables", body, &out), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Saved %s = %s\n", out.Name, show(out.Value))
		return 0
	case len(args) == 3 && args[0] == "delete":
		scope, owner := where(args[1])
		q := url.Values{"scope": {string(scope)}, "owner": {owner}, "name": {args[2]}}
		if code := apiExit(c.Do(ctx, "DELETE", "/v1/variables?"+q.Encode(), nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Deleted %s\n", args[2])
		return 0
	case len(args) > 0 && (args[0] == "set" || args[0] == "delete"):
		fmt.Fprintln(stderr, varsUsage)
		return 2
	}
	fs := flag.NewFlagSet("vars", flag.ContinueOnError)
	fs.SetOutput(stderr)
	search := fs.String("q", "", "only names or viewers containing TEXT")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 {
		fmt.Fprintln(stderr, varsUsage)
		return 2
	}
	q := url.Values{}
	if fs.NArg() == 1 {
		scope, owner := where(fs.Arg(0))
		q.Set("scope", string(scope))
		if owner != "" {
			q.Set("owner", owner)
		}
	}
	if *search != "" {
		q.Set("q", *search)
	}
	var out struct{ Variables []variables.Variable }
	if code := apiExit(c.Do(ctx, "GET", "/v1/variables?"+q.Encode(), nil, &out), stderr); code != 0 {
		return code
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out.Variables)
		return 0
	}
	if len(out.Variables) == 0 {
		fmt.Fprintln(stdout, "No saved variables. Workflows save them with vars.set and vars.add.")
	}
	for _, v := range out.Variables {
		place := string(v.Scope)
		if v.Owner != "" {
			place += ":" + v.Owner
		}
		if v.OwnerLabel != "" {
			place += " (" + v.OwnerLabel + ")"
		}
		fmt.Fprintf(stdout, "%-32s %-24s %s\n", place, v.Name, show(v.Value))
	}
	return 0
}

// show prints a value as JSON, on one line.
func show(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSpace(b.String())
}
