package laya

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecide(t *testing.T) {
	var gotPath, gotAuth string
	var gotReq struct {
		Model     string `json:"model"`
		State     string `json:"state"`
		Questions map[string]struct {
			Type         string         `json:"type"`
			Instructions string         `json:"instructions"`
			Criteria     map[string]any `json:"criteria"`
		} `json:"questions"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		_, _ = io.WriteString(w, `{"answers":{"decision":{"choice":"yes"}}}`)
	}))
	defer server.Close()

	client := New(server.URL, "test-key", "Laya-Q8_0", server.Client())
	choice, err := client.Decide(context.Background(), "the state", "the instructions", map[string]any{"yes": nil, "no": nil})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if choice != "yes" {
		t.Errorf("choice = %q, want yes", choice)
	}
	if gotPath != "/v1/systemone" {
		t.Errorf("path = %q, want /v1/systemone", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if gotReq.Model != "Laya-Q8_0" || gotReq.State != "the state" {
		t.Errorf("model/state = %q/%q, want Laya-Q8_0/the state", gotReq.Model, gotReq.State)
	}
	q, ok := gotReq.Questions["decision"]
	if !ok {
		t.Fatal("no decision question in request")
	}
	if q.Type != "choice" || q.Instructions != "the instructions" {
		t.Errorf("question = %+v", q)
	}
}

func TestDecideUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	client := New(server.URL, "test-key", "m", server.Client())
	if _, err := client.Decide(context.Background(), "s", "i", nil); err == nil {
		t.Fatal("expected error for 502")
	}
}

func TestDecideMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer server.Close()

	client := New(server.URL, "test-key", "m", server.Client())
	if _, err := client.Decide(context.Background(), "s", "i", nil); err == nil {
		t.Fatal("expected error for malformed response")
	}
}

func TestDecideMissingAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"answers":{}}`)
	}))
	defer server.Close()

	client := New(server.URL, "test-key", "m", server.Client())
	choice, err := client.Decide(context.Background(), "s", "i", nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if choice != "" {
		t.Errorf("choice = %q, want empty", choice)
	}
}

func TestEnabled(t *testing.T) {
	if New("u", "", "m", nil).Enabled() {
		t.Error("client without key should be disabled")
	}
	if !New("u", "k", "m", nil).Enabled() {
		t.Error("client with key should be enabled")
	}
}
