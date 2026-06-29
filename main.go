package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/claude-code-tools/mdview-review/internal/render"
	"github.com/claude-code-tools/mdview-review/internal/server"
)

// version is overridden at release time via -ldflags "-X main.version=<tag>".
var version = "dev"

func usage() {
	fmt.Fprintln(os.Stderr, "usage: mdview [--view | --print | --version] <file.md>")
	os.Exit(2)
}

func main() {
	args := os.Args[1:]
	mode := "review"
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			fmt.Println("mdview " + version)
			return
		case "--print":
			mode, args = "print", args[1:]
		case "--view":
			mode, args = "view", args[1:]
		}
	}
	if len(args) < 1 {
		usage()
	}

	src, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}

	switch mode {
	case "view":
		// Overview / FYI: render without the dock, open the browser, return immediately.
		page, err := render.View(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdview: render: %v\n", err)
			os.Exit(1)
		}
		f, err := os.CreateTemp("", "mdview-*.html")
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
			os.Exit(1)
		}
		if _, err := f.WriteString(page); err != nil {
			fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
			os.Exit(1)
		}
		f.Close()
		url := "file://" + f.Name()
		fmt.Fprintf(os.Stderr, "mdview: opened %s\n", url)
		openBrowser(url)
		return

	case "print":
		page, err := render.Page(src, newToken())
		if err != nil {
			fmt.Fprintf(os.Stderr, "mdview: render: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(page)
		return
	}

	// Default: review mode — render with the dock, serve, block until the user decides.
	token := newToken()
	page, err := render.Page(src, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: render: %v\n", err)
		os.Exit(1)
	}
	h, err := server.Start(server.Options{
		Page:            page,
		Token:           token,
		NoClientTimeout: envDur("MDVIEW_NO_CLIENT_SECONDS", 60),
		MaxLifetime:     envDur("MDVIEW_MAX_LIFETIME_SECONDS", 6*3600),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "mdview: review server at %s\n", h.URL)
	openBrowser(h.URL)

	v := h.Wait()
	out, _ := json.Marshal(v)
	fmt.Printf("MDVIEW_VERDICT %s\n", out)
	os.Exit(0)
}

func newToken() string {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}
	return hex.EncodeToString(tok)
}

func envDur(name string, defSeconds float64) time.Duration {
	if s := os.Getenv(name); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return time.Duration(f * float64(time.Second))
		}
	}
	return time.Duration(defSeconds * float64(time.Second))
}

// openBrowser opens url in $MDVIEW_BROWSER / $BROWSER if set (a command, optionally with
// args), otherwise the OS default browser.
func openBrowser(url string) {
	if b := strings.TrimSpace(envFirst("MDVIEW_BROWSER", "BROWSER")); b != "" {
		parts := strings.Fields(b)
		cmd := exec.Command(parts[0], append(parts[1:], url)...)
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "mdview: couldn't launch %q (%v) — open %s manually\n", b, err, url)
		}
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "mdview: couldn't open a browser (%v) — open %s manually\n", err, url)
	}
}

func envFirst(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
