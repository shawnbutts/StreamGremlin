package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/links"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerLinks adds the step behind !link (D-101): a viewer joins their
// Twitch and YouTube accounts, so they share one points balance. It never
// fails for a viewer's mistake: it says what to do in "message", for the
// next step to send.
func registerLinks(reg *workflow.Registry, ls *links.Service) {
	reg.Add(workflow.Action{
		Name: "viewers.link", Label: "Link a viewer's Twitch and YouTube", Group: "Points", External: true, Preview: true, More: true,
		Description: "For !link. \"!link NAME\" names the viewer's account on the other platform (a Twitch name, or a YouTube " +
			"@handle) and gives a code; that account types \"!link CODE\" on its platform within 10 minutes, and the two share " +
			"one points balance. Its output's message says what happened, for a reply. A code typed by anyone else does nothing.",
		Params: []workflow.Param{
			{Name: "text", Label: "What they typed after !link", Type: "text", Editor: "message", DefaultHint: "the command's words"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			a := c.Actor
			if a == nil || a.ID == "" || (a.Platform != links.Twitch && a.Platform != links.YouTube) {
				return nil, errors.New("linking is for a viewer in Twitch or YouTube chat")
			}
			text, ok := c.Args["text"].(string)
			if !ok {
				p, _ := c.Event["payload"].(map[string]any)
				text, _ = p["args"].(string)
			}
			text = strings.TrimSpace(text)
			out := map[string]any{"linked": false}
			if ls == nil && !c.DryRun {
				return nil, errors.New("linking isn't available")
			}
			if c.DryRun {
				out["test"] = true
				out["message"] = "(a test: nothing is linked)"
				return out, nil
			}
			here, there := links.Where(a.Platform), links.Where(map[string]string{links.Twitch: links.YouTube, links.YouTube: links.Twitch}[a.Platform])
			switch {
			case text == "":
				if a.Platform == links.Twitch {
					out["message"] = "To share your points with YouTube, type !link @yourhandle with your YouTube handle."
				} else {
					out["message"] = "To share your points with Twitch, type !link yourname with your Twitch name."
				}
			case links.IsCode(text):
				l, err := ls.Confirm(ctx, events.Actor{Platform: a.Platform, ID: a.ID, Login: a.Login, DisplayName: a.DisplayName}, text)
				if err != nil {
					out["message"] = viewerWords(err)
					return out, nil
				}
				out["linked"], out["link_id"] = true, l.ID
				out["message"] = fmt.Sprintf("Linked! %s on Twitch and %s on YouTube now share one points balance.",
					l.Twitch.DisplayName, l.YouTube.DisplayName)
			default:
				asked, err := ls.Ask(ctx, events.Actor{Platform: a.Platform, ID: a.ID, Login: a.Login, DisplayName: a.DisplayName}, text)
				if err != nil {
					out["message"] = viewerWords(err)
					return out, nil
				}
				out["code"] = asked.Code
				out["message"] = fmt.Sprintf("Now type !link %s in %s chat as %s within 10 minutes to share your points between %s and %s.",
					asked.Code, there, mention(asked), here, there)
			}
			return out, nil
		},
	})
}

func mention(a links.Asked) string {
	return "@" + a.Login
}

// viewerWords is an error in words for chat, without the package's prefix.
func viewerWords(err error) string {
	msg := err.Error()
	for _, p := range []string{"links: invalid: ", "links: already linked: ", "links: that code isn't right: "} {
		if rest, ok := strings.CutPrefix(msg, p); ok {
			if p == "links: that code isn't right: " {
				return "That code isn't right: " + rest + "."
			}
			return strings.ToUpper(rest[:1]) + rest[1:] + "."
		}
	}
	return "Linking didn't work just now; try again in a minute."
}
