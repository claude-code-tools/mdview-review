package render

import (
	"bytes"
	_ "embed"
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

// Page renders markdown source to a full self-contained HTML page with the review UI
// injected and the token substituted into the client script. Mermaid is injected only
// when the rendered body contains a fenced mermaid block.
func Page(src []byte, token string) (string, error) {
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
	b.WriteString("\nbody{box-sizing:border-box;margin:0;padding:2.5rem clamp(1rem,5vw,5rem) 110px;}")
	b.WriteString("\n.mermaid{margin:1rem 0;}\n")
	b.WriteString(reviewCSS)
	b.WriteString(`</style></head><body class="markdown-body">`)
	b.WriteString(body)
	b.WriteString(reviewHTML)
	if hasMermaid {
		b.WriteString("<script>")
		b.WriteString(strings.ReplaceAll(mermaidJS, "</script", `<\/script`))
		b.WriteString("</script><script>")
		b.WriteString(`document.querySelectorAll("pre>code.language-mermaid").forEach(function(c){var d=document.createElement("div");d.className="mermaid";d.textContent=c.textContent;c.parentElement.replaceWith(d);});`)
		b.WriteString(`mermaid.initialize({startOnLoad:false,theme:"default",securityLevel:"loose",maxTextSize:1000000,maxEdges:5000});mermaid.run({querySelector:".mermaid"});`)
		b.WriteString("</script>")
	}
	b.WriteString("<script>")
	b.WriteString(strings.ReplaceAll(reviewJS, "__MDVIEW_TOKEN__", token))
	b.WriteString("</script></body></html>")
	return b.String(), nil
}
