package render

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed assets/github-markdown.css
var githubCSS string

//go:embed assets/mermaid.min.js
var mermaidJS string

//go:embed assets/review.css
var reviewCSS string

//go:embed assets/review.html
var reviewHTML string

//go:embed assets/review.js
var reviewJS string

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// Page renders markdown to a full self-contained HTML page WITH the review dock and the
// curated command strip, with the token + commands substituted into the client script.
func Page(src []byte, token string, commands []Command) (string, error) {
	return build(src, true, token, commands)
}

// View renders markdown to a full self-contained HTML page WITHOUT the review dock —
// for showing the user an overview / FYI that needs no decision.
func View(src []byte) (string, error) {
	return build(src, false, "", nil)
}

func build(src []byte, withReview bool, token string, commands []Command) (string, error) {
	var bodyBuf bytes.Buffer
	if err := md.Convert(src, &bodyBuf); err != nil {
		return "", err
	}
	body := bodyBuf.String()
	hasMermaid := strings.Contains(body, "language-mermaid")

	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>mdview review</title><style>`)
	b.WriteString(githubCSS)
	if withReview {
		// Extra bottom padding so the last lines clear the fixed review dock.
		b.WriteString("\nbody{box-sizing:border-box;margin:0;padding:2.5rem clamp(1rem,5vw,5rem) 160px;}")
		b.WriteString("\n.mermaid{margin:1rem 0;}\n")
		b.WriteString(reviewCSS)
	} else {
		b.WriteString("\nbody{box-sizing:border-box;margin:0;padding:2.5rem clamp(1rem,5vw,5rem);}")
		b.WriteString("\n.mermaid{margin:1rem 0;}\n")
	}
	b.WriteString(`</style></head><body class="markdown-body">`)
	b.WriteString(body)
	if withReview {
		b.WriteString(reviewHTML)
	}
	if hasMermaid {
		b.WriteString("<script>")
		b.WriteString(strings.ReplaceAll(mermaidJS, "</script", `<\/script`))
		b.WriteString("</script><script>")
		b.WriteString(`document.querySelectorAll("pre>code.language-mermaid").forEach(function(c){var d=document.createElement("div");d.className="mermaid";d.textContent=c.textContent;c.parentElement.replaceWith(d);});`)
		b.WriteString(`mermaid.initialize({startOnLoad:false,theme:"default",securityLevel:"strict",maxTextSize:1000000,maxEdges:5000});mermaid.run({querySelector:".mermaid"});`)
		b.WriteString("</script>")
	}
	if withReview {
		cj, err := json.Marshal(commands)
		if err != nil {
			return "", err
		}
		if len(commands) == 0 {
			cj = []byte("[]") // json.Marshal(nil) is "null"; the client expects an array
		}
		js := strings.ReplaceAll(reviewJS, "__MDVIEW_TOKEN__", token)
		js = strings.ReplaceAll(js, "__MDVIEW_COMMANDS__", string(cj))
		b.WriteString("<script>")
		b.WriteString(js)
		b.WriteString("</script>")
	}
	b.WriteString("</body></html>")
	return b.String(), nil
}
