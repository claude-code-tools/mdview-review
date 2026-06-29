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
	"time"

	"github.com/claude-code-tools/mdview-review/internal/render"
	"github.com/claude-code-tools/mdview-review/internal/server"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: mdview [--print] <file.md>")
	os.Exit(2)
}

func main() {
	args := os.Args[1:]
	printOnly := false
	if len(args) > 0 && args[0] == "--print" {
		printOnly = true
		args = args[1:]
	}
	if len(args) < 1 {
		usage()
	}

	src, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}

	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		fmt.Fprintf(os.Stderr, "mdview: %v\n", err)
		os.Exit(1)
	}
	token := hex.EncodeToString(tok)

	page, err := render.Page(src, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdview: render: %v\n", err)
		os.Exit(1)
	}

	// --print renders the self-contained HTML to stdout and exits — no server, no browser.
	if printOnly {
		fmt.Print(page)
		return
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

// envDur reads a fractional-seconds env override, falling back to defSeconds.
func envDur(name string, defSeconds float64) time.Duration {
	if s := os.Getenv(name); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return time.Duration(f * float64(time.Second))
		}
	}
	return time.Duration(defSeconds * float64(time.Second))
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
