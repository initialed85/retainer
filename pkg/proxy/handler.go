// Package proxy forwards OpenAI-compatible requests to an upstream model
// router, augmenting chat prompts with retrieved memory context.
package proxy

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/initialed85/retainer/pkg/memory"
)

// Handler proxies requests to an upstream model router.
type Handler struct {
	Pipeline     *memory.Pipeline
	Upstream     string // base URL, e.g. https://rooter.dev.initialed85.cc
	Token        string // bearer token for the upstream
	SessionID    string // fallback session id when the client sends none
	FrontDoorKey string // inbound bearer key; empty disables the check
	HTTP         *http.Client
	Logger       *log.Logger
}

// HealthPath answers probes without a key. It must be checked before the
// front-door key, because a probe that 401s would restart the pod and wipe
// the context cache - the one thing this service exists to protect.
const HealthPath = "/healthz"

// authorized reports whether the request presents the front-door key.
func authorized(r *http.Request, key string) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1
}

// sessionHeaders are the headers inspected to associate a request with a
// conversation, most specific first.
var sessionHeaders = []string{
	"X-Session-Id",
	"X-Session-Affinity",
	"X-Pi-Session-Id",
	"X-Conversation-Id",
	"X-Thread-Id",
	"Session-Id",
	"Session_id",
	"X-Openrouter-Session-Id",
	"X-Client-Session-Id",
}

// Session returns the session id carried by the request, preferring the
// client's headers over the fallback.
func Session(h http.Header, fallback string) string {
	for _, name := range sessionHeaders {
		if value := strings.TrimSpace(h.Get(name)); value != "" {
			return value
		}
	}
	return strings.TrimSpace(fallback)
}

func hasSessionHeader(h http.Header) bool {
	for _, name := range sessionHeaders {
		if strings.TrimSpace(h.Get(name)) != "" {
			return true
		}
	}
	return false
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == HealthPath {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
		return
	}

	if h.FrontDoorKey != "" && !authorized(r, h.FrontDoorKey) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_key"}`))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err)
		return
	}

	if len(body) > 0 {
		body, err = h.Pipeline.Rewrite(r.Context(), Session(r.Header, h.SessionID), body)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	request, err := http.NewRequestWithContext(r.Context(), r.Method, h.Upstream+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err)
		return
	}

	request.Header = r.Header.Clone()
	if h.SessionID != "" && !hasSessionHeader(request.Header) {
		request.Header.Set("X-Session-Id", h.SessionID)
	}
	request.Header.Set("Authorization", "Bearer "+h.Token)

	response, err := h.client().Do(request)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer response.Body.Close()

	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)

	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			return
		}
		if readErr != nil {
			h.logf("upstream stream: %v", readErr)
			return
		}
	}
}

func (h *Handler) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return http.DefaultClient
}

func (h *Handler) logf(format string, args ...any) {
	if h.Logger != nil {
		h.Logger.Printf(format, args...)
	}
}

// writeError emits a fresh JSON error response. It must only be called
// before the upstream response headers have been written.
func (h *Handler) writeError(w http.ResponseWriter, status int, err error) {
	h.logf("warning: %v", err)
	message, _ := json.Marshal(err.Error())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(append([]byte(`{"error":`), message...), '}'))
}
