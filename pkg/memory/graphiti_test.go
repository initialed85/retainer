package memory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGraphitiSearch(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphiti/search" {
			t.Errorf("path = %q, want /graphiti/search", r.URL.Path)
		}
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"facts":[{"fact":"a"},{"fact":"b"}]}`)
	}))
	defer server.Close()

	client := NewGraphitiClient(server.URL, 10, server.Client())
	raw, err := client.Search(context.Background(), "where is my car?")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.TrimSpace(raw) != `{"facts":[{"fact":"a"},{"fact":"b"}]}` {
		t.Errorf("raw = %q", raw)
	}

	var req struct {
		Query    string `json:"query"`
		MaxFacts int    `json:"max_facts"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if req.Query != "where is my car?" || req.MaxFacts != 10 {
		t.Errorf("request = %+v", req)
	}
}

func TestGraphitiSearchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewGraphitiClient(server.URL, 10, server.Client())
	_, err := client.Search(context.Background(), "q")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want it to mention the status", err)
	}
}

func TestFormatFacts(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"facts", `{"facts":[{"fact":"a"},{"fact":"  b  "},{"fact":"   "}],"extra":1}`, "- a\n- b"},
		{"no facts", `{"facts":[]}`, `{"facts":[]}`},
		{"not json", "  hello world  ", "hello world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatFacts(tc.raw); got != tc.want {
				t.Errorf("FormatFacts(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
