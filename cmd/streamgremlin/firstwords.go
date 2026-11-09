package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/firstwords"
)

const firstWordsUsage = `usage:
  streamgremlin first-words          since when this stream counts, and how many viewers have chatted
  streamgremlin first-words reset    start over: everyone's next message counts as their first words`

func firstWordsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "reset") {
		fmt.Fprintln(stderr, firstWordsUsage)
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var st firstwords.Status
	method, path := "GET", "/v1/first-words"
	if len(args) == 1 {
		method, path = "POST", "/v1/first-words/reset"
	}
	if code := apiExit(c.Do(ctx, method, path, nil, &st), stderr); code != 0 {
		return code
	}
	if method == "POST" {
		fmt.Fprintln(stdout, "Started over: everyone's next message counts as their first words.")
		return 0
	}
	why := map[string]string{"stream start": "you went live", "reset": "you started it over", "12 hours": "12 hours ago (no stream start since)"}[st.Why]
	fmt.Fprintf(stdout, "Counting since %s (%s): %d viewers have said their first words.\n", why, st.Since.Local().Format("Jan 2 15:04"), st.Viewers)
	return 0
}
