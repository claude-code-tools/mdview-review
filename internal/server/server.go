package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Verdict is the outcome of a review, emitted to the session on exit.
type Verdict struct {
	Verdict string `json:"verdict"`
	Comment string `json:"comment,omitempty"`
	Command string `json:"command,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// Options configures a review server.
type Options struct {
	Page            string
	Token           string
	NoClientTimeout time.Duration // dismissed if no SSE client connects in time (default 60s)
	MaxLifetime     time.Duration // dismissed after this regardless (default 6h)
	PPIDPoll        time.Duration // parent-death watchdog interval (default 1s)
	TabCloseGrace   time.Duration // grace before treating all-clients-gone as a tab close (default 1s)
	Nonce           string        // instance id sent over SSE so a reconnecting tab can detect a new server and reload
	OwnerAlive      func() bool   // optional: when it returns false, resolve dismissed (session/owner died)
	StickyPort      int            // preferred port; 0 or unavailable -> random ephemeral
}

// Handle is a running review server.
type Handle struct {
	Port int
	URL  string

	srv   *http.Server
	token string
	page  string
	nonce string
	grace time.Duration

	mu            sync.Mutex
	decided       bool
	clients       int
	everConnected bool
	tabTimer      *time.Timer

	result      chan Verdict
	stop        chan struct{}
	firstClient chan struct{} // closed when the first SSE client connects
}

// Start binds a random localhost port and begins serving the review page.
func Start(o Options) (*Handle, error) {
	if o.NoClientTimeout == 0 {
		o.NoClientTimeout = 60 * time.Second
	}
	if o.MaxLifetime == 0 {
		o.MaxLifetime = 6 * time.Hour
	}
	if o.PPIDPoll == 0 {
		o.PPIDPoll = time.Second
	}
	if o.TabCloseGrace == 0 {
		o.TabCloseGrace = time.Second
	}

	ln, err := listen(o.StickyPort)
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port

	h := &Handle{
		Port:   port,
		URL:    fmt.Sprintf("http://127.0.0.1:%d/", port),
		token:  o.Token,
		page:   o.Page,
		nonce:  o.Nonce,
		grace:  o.TabCloseGrace,
		result:      make(chan Verdict, 1),
		stop:        make(chan struct{}),
		firstClient: make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleRoot)
	mux.HandleFunc("/events", h.handleEvents)
	mux.HandleFunc("/verdict", h.handleVerdict)
	h.srv = &http.Server{Handler: mux}
	go h.srv.Serve(ln)
	go h.lifecycle(o)
	return h, nil
}

// Wait blocks until the review is decided and returns the verdict.
func (h *Handle) Wait() Verdict { return <-h.result }

// FirstClient is closed once the first SSE client connects. The launcher uses it to avoid
// opening a duplicate browser tab when an existing tab has reconnected after a sticky-port reuse.
func (h *Handle) FirstClient() <-chan struct{} { return h.firstClient }

// Dismiss resolves the review as "dismissed" (via the once-only decide path, so it is a no-op
// if a real verdict already landed). The launcher calls it on SIGTERM/interrupt so a superseded
// or interrupted server exits cleanly with a verdict instead of dying by signal.
func (h *Handle) Dismiss() { h.decide(Verdict{Verdict: "dismissed"}) }

// Close shuts the server down. Safe to call after a decision.
func (h *Handle) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return h.srv.Shutdown(ctx)
}

// decide records the first outcome and unblocks Wait. Subsequent calls are no-ops.
func (h *Handle) decide(v Verdict) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.decided {
		return
	}
	h.decided = true
	h.result <- v
	close(h.stop)
}

func (h *Handle) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, h.page)
}

func (h *Handle) handleVerdict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var in Verdict
	_ = json.Unmarshal(body, &in)
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if provided == "" {
		var raw map[string]any
		if json.Unmarshal(body, &raw) == nil {
			if t, ok := raw["token"].(string); ok {
				provided = t
			}
		}
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) != 1 {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var v Verdict
	switch in.Verdict {
	case "approve":
		v = Verdict{Verdict: "approve"}
	case "changes":
		c := strings.TrimSpace(in.Comment)
		if c == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		v = Verdict{Verdict: "changes", Comment: c}
	case "command":
		cmd := strings.TrimSpace(in.Command)
		if cmd == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		v = Verdict{Verdict: "command", Command: cmd, Prompt: in.Prompt}
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Flush the 204 to the client before deciding: Wait() unblocks the moment we decide and
	// main may os.Exit immediately, so flushing guarantees the browser receives the response
	// (and shows "Sent") rather than seeing a truncated connection.
	w.WriteHeader(http.StatusNoContent)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	h.decide(v)
}

// handleEvents is an SSE keep-alive used to detect a closed tab.
func (h *Handle) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flusher", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	io.WriteString(w, ": connected\n\n")
	// Reconnect fast (default EventSource retry is ~3s): after a sticky-port reuse the previous
	// round's tab must reconnect quickly so it reloads and so the launcher can detect it and
	// skip opening a duplicate tab.
	io.WriteString(w, "retry: 400\n\n")
	if h.nonce != "" {
		io.WriteString(w, "event: hello\ndata: "+h.nonce+"\n\n")
	}
	fl.Flush()

	h.mu.Lock()
	first := !h.everConnected
	h.everConnected = true
	h.clients++
	if h.tabTimer != nil {
		h.tabTimer.Stop()
		h.tabTimer = nil
	}
	h.mu.Unlock()
	if first {
		close(h.firstClient) // everConnected only ever goes false->true, so this closes once
	}

	<-r.Context().Done() // client disconnected

	h.mu.Lock()
	h.clients--
	if h.clients <= 0 && !h.decided {
		h.tabTimer = time.AfterFunc(h.grace, func() {
			h.mu.Lock()
			fire := h.clients <= 0 && !h.decided
			h.mu.Unlock()
			if fire {
				h.decide(Verdict{Verdict: "dismissed"})
			}
		})
	}
	h.mu.Unlock()
}

func (h *Handle) lifecycle(o Options) {
	noClient := time.NewTimer(o.NoClientTimeout)
	maxLife := time.NewTimer(o.MaxLifetime)
	ppid := time.NewTicker(o.PPIDPoll)
	defer noClient.Stop()
	defer maxLife.Stop()
	defer ppid.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-noClient.C:
			h.mu.Lock()
			ec := h.everConnected
			h.mu.Unlock()
			if !ec {
				h.decide(Verdict{Verdict: "dismissed"})
			}
		case <-maxLife.C:
			h.decide(Verdict{Verdict: "dismissed"})
		case <-ppid.C:
			if orphaned(os.Getppid()) {
				h.decide(Verdict{Verdict: "dismissed"})
			} else if o.OwnerAlive != nil && !o.OwnerAlive() {
				h.decide(Verdict{Verdict: "dismissed"})
			}
		}
	}
}

// orphaned reports whether the process has been reparented to init (ppid 1), i.e. the
// launching session died. POSIX-only signal; on Windows ppid never becomes 1, so this is a
// no-op there and cleanup relies on the no-client + max-lifetime backstops instead.
func orphaned(ppid int) bool { return ppid == 1 }

// listen prefers stickyPort, falling back to a random ephemeral loopback port (the original
// collision-free behavior) when stickyPort is 0 or already taken.
func listen(stickyPort int) (net.Listener, error) {
	if stickyPort > 0 {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", stickyPort)); err == nil {
			return ln, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}
