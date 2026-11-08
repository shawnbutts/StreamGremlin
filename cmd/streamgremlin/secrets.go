package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
)

func secretsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "forget" {
		return forgetSecret(dir, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "list" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("secrets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "try reading each secret")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unknown secrets command %q; use list or forget\n", fs.Arg(0))
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	path := "/v1/secrets"
	if *check {
		path += "?check=true"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var list api.SecretList
	if err := c.Do(ctx, "GET", path, nil, &list); err != nil {
		reportErr(err, stderr)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(list)
		return 0
	}
	fmt.Fprintf(stdout, "Secrets are kept in %s.\n", list.Store.Label)
	if len(list.Secrets) == 0 {
		fmt.Fprintln(stdout, "None saved yet.")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	if *check {
		fmt.Fprintln(tw, "KEY\tSAVED\tCHECK")
	} else {
		fmt.Fprintln(tw, "KEY\tSAVED")
	}
	bad := 0
	for _, s := range list.Secrets {
		saved := s.UpdatedAt.Local().Format("2006-01-02 15:04")
		if !*check {
			fmt.Fprintf(tw, "%s\t%s\n", s.Key, saved)
			continue
		}
		state := "readable"
		switch s.State {
		case secrets.CheckMissing:
			state, bad = "missing: "+s.Detail, bad+1
		case secrets.CheckUnreadable:
			state, bad = "can't be read: "+s.Detail, bad+1
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Key, saved, state)
	}
	_ = tw.Flush()
	if bad > 0 {
		return 1
	}
	return 0
}

func forgetSecret(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: streamgremlin secrets forget KEY")
		return 2
	}
	key := args[0]
	if !secrets.ValidKey(key) {
		fmt.Fprintf(stderr, "%q isn't a secret's name; see `streamgremlin secrets`\n", key)
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err := c.Do(ctx, "DELETE", "/v1/secrets/"+key, nil, nil); err != nil {
		reportErr(err, stderr)
		return 1
	}
	fmt.Fprintf(stdout, "Forgot %s.\n", key)
	return 0
}
