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

// fencedSearchTerm is what the model returns: a JSON code fence around a
// quoted question.
var fencedSearchTerm = "```json\n" + `"where is my car?"` + "\n" + "```"

type testChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type testChatChoice struct {
	Index        int             `json:"index"`
	Message      testChatMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type testChatResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int              `json:"created"`
	Model   string           `json:"model"`
	Choices []testChatChoice `json:"choices"`
}

func testChatServer(t *testing.T, response string, check func(r *http.Request, body []byte)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if check != nil {
			check(r, body)
		}
		_, _ = io.WriteString(w, response)
	}))
}

func TestExtractSearchTerm(t *testing.T) {
	response, err := json.Marshal(testChatResponse{
		ID: "c1", Object: "chat.completion", Model: "gemma",
		Choices: []testChatChoice{{
			Index:        0,
			Message:      testChatMessage{Role: "assistant", Content: fencedSearchTerm},
			FinishReason: "stop",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotModel, gotReasoning, gotContent string
	server := testChatServer(t, string(response), func(r *http.Request, body []byte) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		var req struct {
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
			Messages        []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("request body is not JSON: %v", err)
			return
		}
		gotModel = req.Model
		gotReasoning = req.ReasoningEffort
		if len(req.Messages) != 1 {
			t.Fatalf("messages = %d, want 1", len(req.Messages))
		}
		gotContent = req.Messages[0].Content
	})
	defer server.Close()

	client := NewSearchTermClient(server.URL+"/v1", "gemma-4b", server.Client())
	term, err := client.ExtractSearchTerm(context.Background(), "  where is my car?  ")
	if err != nil {
		t.Fatalf("ExtractSearchTerm: %v", err)
	}
	if term != "where is my car?" {
		t.Errorf("term = %q, want where is my car?", term)
	}
	if gotModel != "gemma-4b" {
		t.Errorf("model = %q, want gemma-4b", gotModel)
	}
	if gotReasoning != "off" {
		t.Errorf("reasoning_effort = %q, want off", gotReasoning)
	}
	if !strings.Contains(gotContent, "where is my car?") {
		t.Errorf("prompt content = %q, want it to contain the user prompt", gotContent)
	}
}

func TestExtractSearchTermEmptyPrompt(t *testing.T) {
	called := false
	server := testChatServer(t, `{}`, func(r *http.Request, body []byte) { called = true })
	defer server.Close()

	client := NewSearchTermClient(server.URL+"/v1", "", server.Client())
	term, err := client.ExtractSearchTerm(context.Background(), "   ")
	if err != nil {
		t.Fatalf("ExtractSearchTerm: %v", err)
	}
	if term != "" {
		t.Errorf("term = %q, want empty", term)
	}
	if called {
		t.Error("model was called for an empty prompt")
	}
}

func TestExtractSearchTermNoChoices(t *testing.T) {
	response, err := json.Marshal(testChatResponse{ID: "c1", Object: "chat.completion", Model: "gemma"})
	if err != nil {
		t.Fatal(err)
	}
	server := testChatServer(t, string(response), nil)
	defer server.Close()

	client := NewSearchTermClient(server.URL+"/v1", "", server.Client())
	if _, err := client.ExtractSearchTerm(context.Background(), "hello"); err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestCleanSearchTerm(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"where is my car?", "where is my car?"},
		{`"where is my car?"`, "where is my car?"},
		{fencedSearchTerm, "where is my car?"},
		{"  " + fencedSearchTerm + "  ", "where is my car?"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := CleanSearchTerm(tc.in); got != tc.want {
			t.Errorf("CleanSearchTerm(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
