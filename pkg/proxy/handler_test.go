package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/initialed85/retainer/pkg/memory"
)

type stubTerms struct {
	term  string
	calls int
}

func (s *stubTerms) ExtractSearchTerm(ctx context.Context, prompt string) (string, error) {
	s.calls++
	return s.term, nil
}

type stubStore struct {
	raw     string
	queries []string
}

func (s *stubStore) Search(ctx context.Context, query string) (string, error) {
	s.queries = append(s.queries, query)
	return s.raw, nil
}

const chatRequest = `{"model":"m","messages":[{"role":"user","content":"where is my car?"}]}`

func newTestHandler(t *testing.T, upstream *httptest.Server, store *stubStore) (*Handler, *stubTerms) {
	t.Helper()
	terms := &stubTerms{term: "the query"}
	return &Handler{
		Pipeline:     memory.NewPipeline(nil, terms, store, nil),
		Upstream:     upstream.URL,
		Token:        "tok-123",
		SessionID:    "fallback-sess",
		FrontDoorKey: "front-door-key",
		HTTP:         upstream.Client(),
	}, terms
}

const authHeader = "Bearer front-door-key"

func post(t *testing.T, h http.Handler, sessionHeader, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	if sessionHeader != "" {
		name, value, _ := strings.Cut(sessionHeader, ": ")
		req.Header.Set(name, value)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestChatRequestIsAugmentedAndForwarded(t *testing.T) {
	store := &stubStore{raw: `{"facts":[{"fact":"The car is a Honda."}]}`}

	var gotMethod, gotPath, gotRawQuery, gotAuth, gotSession string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotSession = r.Header.Get("X-Session-Id")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream", "yes")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()

	h, terms := newTestHandler(t, upstream, store)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?x=1", strings.NewReader(chatRequest))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "hi") {
		t.Errorf("response body = %s", rr.Body.String())
	}
	if rr.Header().Get("X-Upstream") != "yes" {
		t.Errorf("upstream response headers were not forwarded")
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/chat/completions" || gotRawQuery != "x=1" {
		t.Errorf("forwarded %s %s?%s", gotMethod, gotPath, gotRawQuery)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want Bearer tok-123", gotAuth)
	}
	if gotSession != "fallback-sess" {
		t.Errorf("X-Session-Id = %q, want fallback-sess", gotSession)
	}
	if terms.calls != 1 {
		t.Errorf("term calls = %d, want 1", terms.calls)
	}
	if len(store.queries) != 1 || store.queries[0] != "the query" {
		t.Errorf("queries = %v", store.queries)
	}

	var forwarded struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &forwarded); err != nil {
		t.Fatalf("forwarded body is not JSON: %v", err)
	}
	if len(forwarded.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(forwarded.Messages))
	}
	content := forwarded.Messages[0].Content
	wantPrefix := "where is my car?\n\n" + memory.RetrievedContextMarker + "\n"
	if !strings.HasPrefix(content, wantPrefix) {
		t.Errorf("content = %q, want prefix %q", content, wantPrefix)
	}
	if !strings.Contains(content, "- The car is a Honda.") || !strings.HasSuffix(content, "</retrieved_context>") {
		t.Errorf("content = %q", content)
	}
}

func TestClientSessionHeaderWins(t *testing.T) {
	store := &stubStore{raw: `{}`}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The client's own session header is forwarded as-is; the fallback
		// must not be added on top of it.
		if got := r.Header.Get("X-Pi-Session-Id"); got != "client-sess" {
			t.Errorf("X-Pi-Session-Id = %q, want client-sess", got)
		}
		if got := r.Header.Get("X-Session-Id"); got != "" {
			t.Errorf("X-Session-Id = %q, want empty", got)
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	h, _ := newTestHandler(t, upstream, store)
	post(t, h, "X-Pi-Session-Id: client-sess", chatRequest)
}

func TestSessionIsolatesCache(t *testing.T) {
	store := &stubStore{raw: `{"facts":[{"fact":"a"}]}`}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	h, _ := newTestHandler(t, upstream, store)
	post(t, h, "X-Pi-Session-Id: client-a", chatRequest)
	post(t, h, "X-Pi-Session-Id: client-b", chatRequest)
	if len(store.queries) != 2 {
		t.Fatalf("queries = %d, want 2 (one per session)", len(store.queries))
	}
	post(t, h, "X-Pi-Session-Id: client-a", chatRequest)
	if len(store.queries) != 2 {
		t.Errorf("queries = %d, want 2 (client-a should be cached)", len(store.queries))
	}
}

func TestHistoryReplay(t *testing.T) {
	store := &stubStore{raw: `{"facts":[{"fact":"a"}]}`}

	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	h, _ := newTestHandler(t, upstream, store)
	turn1 := `{"model":"m","messages":[{"role":"user","content":"A"}]}`
	turn2 := `{"model":"m","messages":[{"role":"user","content":"A"},{"role":"assistant","content":"ok"},{"role":"user","content":"B"}]}`
	post(t, h, "", turn1)
	post(t, h, "", turn2)

	if len(store.queries) != 2 {
		t.Fatalf("queries = %v, want 2 (A once, B once)", store.queries)
	}

	var turn2Req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(bodies[len(bodies)-1]), &turn2Req); err != nil {
		t.Fatalf("turn 2 body is not JSON: %v", err)
	}
	if len(turn2Req.Messages) != 3 {
		t.Fatalf("turn 2 messages = %d, want 3", len(turn2Req.Messages))
	}
	if !strings.Contains(turn2Req.Messages[0].Content, "A\n\n"+memory.RetrievedContextMarker) {
		t.Errorf("turn 2 did not replay A's cached context: %q", turn2Req.Messages[0].Content)
	}
	if !strings.Contains(turn2Req.Messages[2].Content, "B\n\n"+memory.RetrievedContextMarker) {
		t.Errorf("turn 2 did not inject B's fresh context: %q", turn2Req.Messages[2].Content)
	}
}

func TestNonChatRequestPassesThrough(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	h, _ := newTestHandler(t, upstream, &stubStore{})
	rr := post(t, h, "", "not json")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if string(gotBody) != "not json" {
		t.Errorf("forwarded body = %q, want unchanged", string(gotBody))
	}
}

func TestUpstreamFailureReturns500(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := upstream.URL
	upstream.Close()

	h := &Handler{
		Pipeline: memory.NewPipeline(nil, &stubTerms{}, &stubStore{}, nil),
		Upstream: url,
		Token:    "t",
	}
	rr := post(t, h, "", chatRequest)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"error"`) {
		t.Errorf("body = %s, want a JSON error", rr.Body.String())
	}
}

func TestStreamingResponseIsCopied(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "chunk-one")
		flusher.Flush()
		_, _ = io.WriteString(w, "chunk-two")
		flusher.Flush()
	}))
	defer upstream.Close()

	h, _ := newTestHandler(t, upstream, &stubStore{})
	rr := post(t, h, "", chatRequest)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Body.String(); got != "chunk-onechunk-two" {
		t.Errorf("body = %q, want chunk-onechunk-two", got)
	}
}

func TestFrontDoorKeyRequired(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream was contacted despite missing key")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	h, _ := newTestHandler(t, upstream, &stubStore{})

	// No key at all.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chatRequest))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized || rr.Body.String() != `{"error":"invalid_key"}` {
		t.Errorf("no key: status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Wrong key.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chatRequest))
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: status = %d, want 401", rr.Code)
	}

	// Non-Bearer scheme.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chatRequest))
	req.Header.Set("Authorization", "front-door-key")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("bare key: status = %d, want 401", rr.Code)
	}
}

func TestHealthzWithoutKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream was contacted for a health probe")
	}))
	defer upstream.Close()

	h, _ := newTestHandler(t, upstream, &stubStore{})

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(method, HealthPath, nil)
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s %s: status = %d, want 200", method, HealthPath, rr.Code)
		}
	}
}

func TestNoKeyCheckWhenFrontDoorKeyEmpty(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	h, _ := newTestHandler(t, upstream, &stubStore{})
	h.FrontDoorKey = ""
	rr := post(t, h, "", chatRequest)
	// post() sends the front-door key, but with the check disabled it must be
	// ignored and the request forwarded.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}
