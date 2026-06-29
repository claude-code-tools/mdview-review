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

func TestViewOmitsReviewDock(t *testing.T) {
	out, err := View([]byte("# Title\n\nHello"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "<h1") {
		t.Error("view should still render the document body")
	}
	if strings.Contains(out, "mdview-bar") {
		t.Error("view must omit the review dock")
	}
	if strings.Contains(out, "__MDVIEW_TOKEN__") {
		t.Error("view must not carry a token placeholder")
	}
}

func TestViewKeepsMermaid(t *testing.T) {
	withM, _ := View([]byte("```mermaid\ngraph TD;A-->B\n```\n"))
	if !strings.Contains(withM, "mermaid.initialize") {
		t.Error("view should still render mermaid diagrams")
	}
}

func TestPageWiresReloadOnReconnect(t *testing.T) {
	page, err := Page([]byte("# hi"), "tok")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`addEventListener("hello"`, "location.reload()"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q", want)
		}
	}
}
