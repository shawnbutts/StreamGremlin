package main

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// markdown turns the repository's own Markdown into HTML: the changelog,
// SECURITY.md and the beta guide. It knows only what those use: ATX and
// underlined headings, paragraphs, nested "-" and "1." lists with
// continuation lines, and inline code, bold, italics and links. Everything
// is escaped first, so a file can't put raw HTML on the site. A link to a
// file in the repository (not http or https) becomes its plain text, since
// the site doesn't carry those files. shift moves headings down, so a
// file's "#" can sit under the page's own h1.
func markdown(src string, shift int) string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out strings.Builder
	var para []string
	type list struct {
		indent  int
		ordered bool
	}
	var lists []list
	itemOpen := false

	flushPara := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	closeItem := func() {
		if itemOpen {
			out.WriteString("</li>\n")
			itemOpen = false
		}
	}
	closeLists := func(toIndent int) {
		for len(lists) > 0 && lists[len(lists)-1].indent > toIndent {
			closeItem()
			if lists[len(lists)-1].ordered {
				out.WriteString("</ol>\n")
			} else {
				out.WriteString("</ul>\n")
			}
			lists = lists[:len(lists)-1]
			itemOpen = len(lists) > 0 // the parent's item is still open
		}
	}
	heading := func(level int, text string) {
		flushPara()
		closeLists(-1)
		level += shift
		if level > 6 {
			level = 6
		}
		id := slug(text)
		tag := "h" + string(rune('0'+level))
		out.WriteString("<" + tag + ` id="` + id + `">` + inline(text) + "</" + tag + ">\n")
	}

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trim := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if trim == "" {
			flushPara()
			// A blank line ends the lists unless an item or an indented
			// line of the same list comes next.
			if len(lists) > 0 {
				j := i + 1
				for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
					j++
				}
				if j == len(lines) {
					closeLists(-1)
				} else if m := item.FindStringSubmatch(lines[j]); m == nil || len(m[1]) == 0 && (m[2] != "-" && m[2] != "*") != lists[0].ordered {
					if m == nil && strings.HasPrefix(lines[j], " ") {
						continue
					}
					closeLists(-1)
				}
			}
			continue
		}
		// Underlined headings: the next line is all = or all -.
		if i+1 < len(lines) && len(lists) == 0 && len(para) == 0 {
			next := strings.TrimSpace(lines[i+1])
			if len(next) >= 3 && (strings.Trim(next, "=") == "" || strings.Trim(next, "-") == "") {
				level := 2
				if next[0] == '=' {
					level = 1
				}
				heading(level, trim)
				i++
				continue
			}
		}
		if m := atx.FindStringSubmatch(trim); m != nil && indent == 0 {
			heading(len(m[1]), m[2])
			continue
		}
		if m := item.FindStringSubmatch(line); m != nil {
			flushPara()
			ordered := m[2] != "-" && m[2] != "*"
			switch {
			case len(lists) == 0 || indent > lists[len(lists)-1].indent:
				if ordered {
					out.WriteString("<ol>\n")
				} else {
					out.WriteString("<ul>\n")
				}
				lists = append(lists, list{indent, ordered})
				itemOpen = false
			default:
				closeLists(indent)
				closeItem()
				if top := lists[len(lists)-1]; top.ordered != ordered {
					// Another kind of list at the same depth: a new list.
					if top.ordered {
						out.WriteString("</ol>\n")
					} else {
						out.WriteString("</ul>\n")
					}
					if ordered {
						out.WriteString("<ol>\n")
					} else {
						out.WriteString("<ul>\n")
					}
					lists[len(lists)-1].ordered = ordered
				}
			}
			out.WriteString("<li>")
			itemOpen = true
			// The item's text runs on over indented lines that aren't items.
			text := []string{m[3]}
			for i+1 < len(lines) {
				next := lines[i+1]
				nt := strings.TrimSpace(next)
				ni := len(next) - len(strings.TrimLeft(next, " "))
				if nt == "" || ni <= indent || item.MatchString(next) {
					break
				}
				text = append(text, nt)
				i++
			}
			out.WriteString(inline(strings.Join(text, " ")))
			continue
		}
		if len(lists) > 0 && indent == 0 {
			closeLists(-1)
		}
		para = append(para, trim)
	}
	flushPara()
	closeLists(-1)
	return out.String()
}

var (
	atx  = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*$`)
	item = regexp.MustCompile(`^(\s*)([-*]|\d+\.)\s+(.*)$`)

	code   = regexp.MustCompile("`([^`]+)`")
	bold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italic = regexp.MustCompile(`(^|[^\w*])\*([^*\s][^*]*)\*`)
	link   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
)

// inline escapes text, then marks up code spans (whose insides are left
// alone), links, bold and italics.
func inline(s string) string {
	var spans []string
	s = code.ReplaceAllStringFunc(s, func(m string) string {
		spans = append(spans, "<code>"+html.EscapeString(m[1:len(m)-1])+"</code>")
		return "\x00" + strconv.Itoa(len(spans)-1) + "\x00"
	})
	s = html.EscapeString(s)
	s = link.ReplaceAllStringFunc(s, func(m string) string {
		p := link.FindStringSubmatch(m)
		href := html.UnescapeString(p[2])
		if strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://") {
			return `<a href="` + html.EscapeString(href) + `">` + p[1] + "</a>"
		}
		return p[1]
	})
	s = bold.ReplaceAllString(s, "<strong>$1</strong>")
	s = italic.ReplaceAllString(s, "$1<em>$2</em>")
	for i, span := range spans {
		s = strings.Replace(s, "\x00"+strconv.Itoa(i)+"\x00", span, 1)
	}
	return s
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug is a heading's id: lowercase words joined by hyphens.
func slug(s string) string {
	s = code.ReplaceAllString(s, "$1")
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}
