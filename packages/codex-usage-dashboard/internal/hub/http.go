package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"codex-usage-dashboard/internal/model"
)

type HTTPHandler struct {
	Hub           *Hub
	Assets        fs.FS
	AllowedHosts  []string
	eventSequence *atomic.Uint64
	sseSlots      chan struct{}
}

func (h HTTPHandler) Handler() (http.Handler, error) {
	allowedHosts, err := newAllowedHostSet(h.AllowedHosts)
	if err != nil {
		return nil, err
	}
	if h.eventSequence == nil {
		h.eventSequence = &atomic.Uint64{}
	}
	if h.sseSlots == nil {
		h.sseSlots = make(chan struct{}, 64)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.health)
	mux.HandleFunc("/api/v1/status", h.status)
	mux.HandleFunc("/api/v1/events", h.events)
	if h.Assets != nil {
		if assets, err := fs.Sub(h.Assets, "assets"); err == nil {
			mux.Handle("/assets/", onlyReadMethods(http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))))
		}
		mux.HandleFunc("/", h.index)
	}
	return securityHeaders(allowedHosts.handler(mux)), nil
}

func (h HTTPHandler) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	}
}

func (h HTTPHandler) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(h.Hub.Status())
}

func (h HTTPHandler) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	select {
	case h.sseSlots <- struct{}{}:
		defer func() { <-h.sseSlots }()
	default:
		http.Error(w, "too many event streams", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	updates, cancel := h.Hub.Subscribe()
	defer cancel()

	refreshEvery := h.Hub.staleAfter / 2
	if refreshEvery <= 0 || refreshEvery > 25*time.Second {
		refreshEvery = 25 * time.Second
	}
	keepAlive := time.NewTicker(refreshEvery)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case update, open := <-updates:
			if !open {
				return
			}
			if err := h.writeSSE(w, flusher, update); err != nil {
				return
			}
		case <-keepAlive.C:
			update := h.Hub.Status()
			if err := h.writeSSE(w, flusher, update); err != nil {
				return
			}
		}
	}
}

func (h HTTPHandler) writeSSE(w http.ResponseWriter, flusher http.Flusher, update model.StatusResponse) error {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	payload, err := json.Marshal(update)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: snapshot\ndata: %s\n\n", h.eventSequence.Add(1), payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (h HTTPHandler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	index, err := fs.ReadFile(h.Assets, "index.html")
	if err != nil {
		http.Error(w, "site unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodGet {
		_, _ = w.Write(index)
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func onlyReadMethods(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, "GET, HEAD")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Cache-Control", "no-store")
		headers.Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'none'",
			"base-uri 'none'",
			"connect-src 'self'",
			"font-src 'self'",
			"form-action 'none'",
			"frame-ancestors 'none'",
			"img-src 'self' data:",
			"object-src 'none'",
			"script-src 'self'",
			"style-src 'self'",
		}, "; "))
		headers.Set("Cross-Origin-Opener-Policy", "same-origin")
		headers.Set("Cross-Origin-Resource-Policy", "same-origin")
		headers.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
