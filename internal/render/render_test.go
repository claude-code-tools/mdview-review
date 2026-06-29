package render

import (
	"strings"
	"testing"
)

func TestPageInjectsTokenAndBar(t *testing.T) {
	out, err := Page([]byte("# Title\n\nHello"), "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="mdview-bar"`, "deadbeef", "<h1", "Hello"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "__MDVIEW_TOKEN__") {
		t.Error("placeholder not replaced")
	}
}

func TestPageMermaidConditional(t *testing.T) {
	plain, _ := Page([]byte("# x"), "t")
	if strings.Contains(plain, "mermaid.initialize") {
		t.Error("mermaid injected without fence")
	}
	withM, _ := Page([]byte("```mermaid\ngraph TD;A-->B\n```\n"), "t")
	if !strings.Contains(withM, "mermaid.initialize") {
		t.Error("mermaid not injected with fence")
	}
}
