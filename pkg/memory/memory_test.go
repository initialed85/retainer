package memory

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
)

type fakeDecider struct {
	skipSearch   bool
	include      bool
	fail         bool
	searchCalls  int
	includeCalls int
}

func (f *fakeDecider) Decide(ctx context.Context, state, instructions string, criteria map[string]any) (string, error) {
	if f.fail {
		return "", errors.New("decider unavailable")
	}
	if strings.Contains(instructions, "skipped for memory search") {
		f.searchCalls++
		if f.skipSearch {
			return "yes", nil
		}
		return "no", nil
	}
	f.includeCalls++
	if f.include {
		return "yes", nil
	}
	return "no", nil
}

type fakeTerms struct {
	term  string
	echo  bool
	err   error
	calls int
}

func (f *fakeTerms) ExtractSearchTerm(ctx context.Context, prompt string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	if f.echo {
		return prompt, nil
	}
	return f.term, nil
}

type fakeStore struct {
	raw     string
	rawFor  func(query string) string
	err     error
	queries []string
}

func (f *fakeStore) Search(ctx context.Context, query string) (string, error) {
	f.queries = append(f.queries, query)
	if f.rawFor != nil {
		return f.rawFor(query), f.err
	}
	return f.raw, f.err
}

func quietLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestRetrieveHappyPath(t *testing.T) {
	terms := &fakeTerms{term: "where is my car?"}
	store := &fakeStore{raw: `{"facts":[{"fact":"The car is a Honda."}]}`}
	p := NewPipeline(nil, terms, store, quietLogger())

	got, err := p.Retrieve(context.Background(), "s1", "  where is my car parked?  ")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != "- The car is a Honda." {
		t.Errorf("context = %q", got)
	}
	if len(store.queries) != 1 || store.queries[0] != "where is my car?" {
		t.Errorf("queries = %v", store.queries)
	}

	// Cached: no second extraction or search.
	if _, err := p.Retrieve(context.Background(), "s1", "where is my car parked?"); err != nil {
		t.Fatalf("second Retrieve: %v", err)
	}
	if len(store.queries) != 1 || terms.calls != 1 {
		t.Errorf("cache miss: store calls %d, term calls %d", len(store.queries), terms.calls)
	}
}

func TestRetrieveSearchGateSkips(t *testing.T) {
	decider := &fakeDecider{skipSearch: true}
	terms := &fakeTerms{}
	store := &fakeStore{}
	p := NewPipeline(decider, terms, store, quietLogger())

	got, err := p.Retrieve(context.Background(), "s1", "hi")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != "" {
		t.Errorf("context = %q, want empty", got)
	}
	if terms.calls != 0 || len(store.queries) != 0 {
		t.Errorf("search ran anyway: %d terms, %d queries", terms.calls, len(store.queries))
	}

	// The negative result is cached: the gate is not re-asked.
	if _, err := p.Retrieve(context.Background(), "s1", "hi"); err != nil {
		t.Fatalf("second Retrieve: %v", err)
	}
	if decider.searchCalls != 1 {
		t.Errorf("searchCalls = %d, want 1", decider.searchCalls)
	}
}

func TestRetrieveIncludeGateDrops(t *testing.T) {
	decider := &fakeDecider{include: false}
	terms := &fakeTerms{term: "q"}
	store := &fakeStore{raw: `{"facts":[{"fact":"a"}]}`}
	p := NewPipeline(decider, terms, store, quietLogger())

	got, err := p.Retrieve(context.Background(), "s1", "p")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != "" {
		t.Errorf("context = %q, want empty", got)
	}
	if decider.includeCalls != 1 {
		t.Errorf("includeCalls = %d, want 1", decider.includeCalls)
	}
}

func TestRetrieveGatesOpenOnDeciderFailure(t *testing.T) {
	decider := &fakeDecider{fail: true}
	terms := &fakeTerms{term: "q"}
	store := &fakeStore{raw: `{"facts":[{"fact":"a"}]}`}
	p := NewPipeline(decider, terms, store, quietLogger())

	got, err := p.Retrieve(context.Background(), "s1", "p")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != "- a" {
		t.Errorf("context = %q, want - a", got)
	}
}

func TestRetrieveEmptyTerm(t *testing.T) {
	terms := &fakeTerms{term: ""}
	store := &fakeStore{}
	p := NewPipeline(nil, terms, store, quietLogger())

	got, err := p.Retrieve(context.Background(), "s1", "p")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != "" {
		t.Errorf("context = %q, want empty", got)
	}
	if len(store.queries) != 0 {
		t.Errorf("store was called")
	}

	// The negative result is cached: no re-extraction.
	if _, err := p.Retrieve(context.Background(), "s1", "p"); err != nil {
		t.Fatalf("second Retrieve: %v", err)
	}
	if terms.calls != 1 {
		t.Errorf("term calls = %d, want 1", terms.calls)
	}
}

func TestRetrieveTermExtractionFailureNotCached(t *testing.T) {
	terms := &fakeTerms{err: errors.New("gemma down")}
	store := &fakeStore{}
	p := NewPipeline(nil, terms, store, quietLogger())

	got, err := p.Retrieve(context.Background(), "s1", "p")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != "" {
		t.Errorf("context = %q, want empty (degraded)", got)
	}

	if _, err := p.Retrieve(context.Background(), "s1", "p"); err != nil {
		t.Fatalf("second Retrieve: %v", err)
	}
	if terms.calls != 2 {
		t.Errorf("term calls = %d, want 2 (retry)", terms.calls)
	}
}

func TestRetrieveStoreFailureNotCached(t *testing.T) {
	terms := &fakeTerms{term: "q"}
	store := &fakeStore{err: errors.New("graphiti down")}
	p := NewPipeline(nil, terms, store, quietLogger())

	got, err := p.Retrieve(context.Background(), "s1", "p")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != "" {
		t.Errorf("context = %q, want empty (degraded)", got)
	}

	if _, err := p.Retrieve(context.Background(), "s1", "p"); err != nil {
		t.Fatalf("second Retrieve: %v", err)
	}
	if len(store.queries) != 2 {
		t.Errorf("store calls = %d, want 2 (retry)", len(store.queries))
	}
}

func TestRetrieveSessionIsolation(t *testing.T) {
	terms := &fakeTerms{term: "q"}
	store := &fakeStore{raw: `{"facts":[{"fact":"a"}]}`}
	p := NewPipeline(nil, terms, store, quietLogger())

	if _, err := p.Retrieve(context.Background(), "s1", "same prompt"); err != nil {
		t.Fatalf("Retrieve s1: %v", err)
	}
	if _, err := p.Retrieve(context.Background(), "s2", "same prompt"); err != nil {
		t.Fatalf("Retrieve s2: %v", err)
	}
	if len(store.queries) != 2 {
		t.Errorf("store calls = %d, want 2 (one per session)", len(store.queries))
	}
}

func TestRetrieveUnstructuredResult(t *testing.T) {
	terms := &fakeTerms{term: "q"}
	store := &fakeStore{raw: "  a plain note  "}
	p := NewPipeline(nil, terms, store, quietLogger())

	got, err := p.Retrieve(context.Background(), "s1", "p")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got != "a plain note" {
		t.Errorf("context = %q, want a plain note", got)
	}
}

func TestContextCache(t *testing.T) {
	c := NewContextCache()
	if _, ok := c.Get("s", "p"); ok {
		t.Fatal("expected miss on empty cache")
	}

	c.Put("s", "p", "ctx")
	if got, ok := c.Get("s", "p"); !ok || got != "ctx" {
		t.Fatalf("Get(s,p) = %q, %v", got, ok)
	}
	if _, ok := c.Get("s2", "p"); ok {
		t.Fatal("expected miss for different session")
	}
	if _, ok := c.Get("s", "other"); ok {
		t.Fatal("expected miss for different prompt")
	}
	if _, ok := c.Get("s", "   p   "); !ok {
		t.Fatal("expected hit for whitespace-equivalent prompt")
	}

	c.Put("s", "n", "")
	if got, ok := c.Get("s", "n"); !ok || got != "" {
		t.Fatalf("negative cache: Get = %q, %v", got, ok)
	}
}
