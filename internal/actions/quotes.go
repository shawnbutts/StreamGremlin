package actions

import (
	"context"
	"errors"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/quotes"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// quoteOut is what a quote step gives the steps after it.
func quoteOut(q quotes.Quote, count int) map[string]any {
	return map[string]any{
		"found": true, "id": q.ID, "text": q.Text, "by": q.AddedBy, "category": q.Category,
		"date": q.CreatedAt.Local().Format("2 Jan 2006"), "created_at": q.CreatedAt.UTC().Format(time.RFC3339), "count": count,
	}
}

// registerQuotes adds the quote steps (docs/adr/0014-quotes.md). They change
// StreamGremlin's own data, so they run for test events too, but then only
// say what they would do.
func registerQuotes(reg *workflow.Registry, qs *quotes.Store) {
	// number reads the number chat typed: given is false when it's empty,
	// ok is false when it isn't a number (said in chat, not a failure).
	number := func(c workflow.Call) (n int64, given, ok bool) {
		s, _ := c.String("number", "")
		if strings.TrimSpace(s) == "" {
			return 0, false, true
		}
		n, ok = quotes.ParseNumber(s)
		return n, true, ok
	}
	reg.Add(workflow.Action{
		Name: "quotes.add", Label: "Add a quote", Group: "Quotes", SavesState: true,
		Description: "Saves a quote with the next number and gives back its number. A retried run adds it once.",
		Params: []workflow.Param{
			{Name: "text", Label: "The quote", Type: "text", Required: true, Editor: "message"},
			{Name: "by", Label: "Added by", Type: "text", Editor: "message", DefaultHint: "The viewer who typed the command, or you"},
			{Name: "category", Label: "Category", Type: "text", Editor: "message", DefaultHint: "None"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			text, _ := c.String("text", "")
			by, _ := c.String("by", "")
			category, _ := c.String("category", "")
			n := quotes.New{Text: text, AddedBy: by, Category: category, Key: "job:" + c.IdempotencyKey}
			if v := c.Viewer(); v != nil {
				n.AddedByID, n.Platform = v.Platform+":"+v.ID, v.Platform
				if n.AddedBy == "" {
					n.AddedBy = v.DisplayName
				}
			}
			if n.AddedBy == "" {
				n.AddedBy = "you"
			}
			if c.DryRun {
				next, err := qs.NextNumber(ctx)
				if err != nil {
					return nil, err
				}
				count, _ := qs.Count(ctx)
				out := quoteOut(quotes.Quote{ID: next, Text: strings.Join(strings.Fields(text), " "), AddedBy: n.AddedBy, Category: category, CreatedAt: time.Now()}, count+1)
				out["test"] = true
				return out, nil
			}
			q, _, err := qs.Add(ctx, n, time.Now())
			if err != nil {
				return nil, err
			}
			count, _ := qs.Count(ctx)
			return quoteOut(q, count), nil
		},
	})
	reg.Add(workflow.Action{
		Name: "quotes.get", Label: "Pick a quote", Group: "Quotes",
		Description: "A quote by its number, or at random when the number is empty. When there's no such quote, found is false and the run goes on.",
		Params: []workflow.Param{
			{Name: "number", Label: "Number", Type: "text", DefaultHint: "Empty: one at random"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			count, err := qs.Count(ctx)
			if err != nil {
				return nil, err
			}
			n, byNumber, ok := number(c)
			if !ok {
				// Not a failure: the workflow says so in chat.
				return map[string]any{"found": false, "id": 0, "count": count, "error": "that isn't a quote number"}, nil
			}
			var q quotes.Quote
			if byNumber {
				q, err = qs.Get(ctx, n)
			} else {
				q, err = qs.Random(ctx)
			}
			if errors.Is(err, quotes.ErrNotFound) {
				return map[string]any{"found": false, "id": n, "count": count}, nil
			}
			if err != nil {
				return nil, err
			}
			return quoteOut(q, count), nil
		},
	})
	reg.Add(workflow.Action{
		Name: "quotes.delete", Label: "Delete a quote", Group: "Quotes", SavesState: true, More: true,
		Description: "Deletes a quote by its number. Later quotes keep their numbers. deleted is false when there was none.",
		Params: []workflow.Param{
			{Name: "number", Label: "Number", Type: "text", Required: true},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			n, given, ok := number(c)
			if !given || !ok {
				return map[string]any{"deleted": false, "id": 0, "error": "that isn't a quote number"}, nil
			}
			if c.DryRun {
				_, err := qs.Get(ctx, n)
				return map[string]any{"deleted": err == nil, "id": n, "test": true}, nil
			}
			deleted, err := qs.Delete(ctx, n)
			if err != nil {
				return nil, err
			}
			return map[string]any{"deleted": deleted, "id": n}, nil
		},
	})
}
