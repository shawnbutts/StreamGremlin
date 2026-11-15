package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
)

const raidGuardUsage = `usage:
  streamgremlin raidguard                 the raid guard's settings, and the latest warning
  streamgremlin raidguard set [mode=off|alert|lock] [first=N] [new=N] [days=N] [window=SECONDS] [after-raid=MINUTES]
  streamgremlin raidguard test            a test warning on Live (nothing is locked)
  streamgremlin raidguard dismiss         hide the warning on Live

A burst is that many first-time chatters, or Twitch accounts younger than
days, within window seconds; for after-raid minutes after a raid, half as
many. alert warns on Live and starts "A raid looks suspicious"; lock also
locks chat. It never unlocks on its own.`

func raidGuardCmd(dir string, args []string, stdout, stderr io.Writer) int {
	sub := "show"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	if sub == "help" || sub == "-h" || sub == "--help" {
		fmt.Fprintln(stdout, raidGuardUsage)
		return 0
	}
	usage := func() int { fmt.Fprintln(stderr, raidGuardUsage); return 2 }
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var v api.RaidGuardView
	switch sub {
	case "show":
		if len(args) != 0 {
			return usage()
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/raid-guard", nil, &v), stderr); code != 0 {
			return code
		}
	case "set":
		if code := apiExit(c.Do(ctx, "GET", "/v1/raid-guard", nil, &v), stderr); code != 0 {
			return code
		}
		st := v.Settings
		for _, a := range args {
			k, val, ok := strings.Cut(a, "=")
			if !ok {
				return usage()
			}
			if k == "mode" {
				st.Mode = val
				continue
			}
			n, err := strconv.Atoi(val)
			if err != nil {
				return usage()
			}
			switch k {
			case "first":
				st.FirstTime = n
			case "new":
				st.NewAccounts = n
			case "days":
				st.Days = n
			case "window":
				st.Window = n
			case "after-raid":
				st.AfterRaid = n
			default:
				return usage()
			}
		}
		if code := apiExit(c.Do(ctx, "PUT", "/v1/raid-guard", st, &v), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "Saved.")
	case "test", "dismiss":
		if code := apiExit(c.Do(ctx, "POST", "/v1/raid-guard/"+sub, nil, &v), stderr); code != 0 {
			return code
		}
		if sub == "test" {
			fmt.Fprintln(stdout, "A test warning is on Live. Nothing was locked.")
			return 0
		}
		fmt.Fprintln(stdout, "Dismissed.")
		return 0
	default:
		return usage()
	}
	st := v.Settings
	mode := map[string]string{"off": "off", "alert": "warns on Live", "lock": "warns on Live and locks chat"}[st.Mode]
	fmt.Fprintf(stdout, "Raid guard: %s.\n", mode)
	fmt.Fprintf(stdout, "  A burst: %d first-time chatters, or %d Twitch accounts younger than %d days, within %d seconds.\n",
		st.FirstTime, st.NewAccounts, st.Days, st.Window)
	if st.AfterRaid > 0 {
		fmt.Fprintf(stdout, "  For %d minutes after a raid, half as many.\n", st.AfterRaid)
	}
	if a := v.Alert; a != nil {
		what := map[string]string{"first_time": "first-time chatters", "new_accounts": "brand-new accounts"}[a.Reason]
		test := ""
		if a.Test {
			test = " (a test)"
		}
		fmt.Fprintf(stdout, "Latest warning%s, %s: %d %s in %d seconds", test, a.At.Local().Format("15:04:05"), a.Count, what, a.Window)
		if a.Locked {
			fmt.Fprint(stdout, "; chat was locked")
		}
		fmt.Fprintln(stdout, ".")
		var names []string
		for _, x := range a.Accounts {
			names = append(names, x.DisplayName)
		}
		fmt.Fprintln(stdout, "  "+strings.Join(names, ", "))
	}
	return 0
}
