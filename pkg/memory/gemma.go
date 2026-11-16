package memory

import (
	"context"
	"errors"
	"net/http"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// SearchTermer extracts a memory search query from a user prompt.
type SearchTermer interface {
	ExtractSearchTerm(ctx context.Context, prompt string) (string, error)
}

// SearchTermClient derives a memory search query from a prompt using an
// OpenAI-compatible chat endpoint (local gemma).
type SearchTermClient struct {
	baseURL string
	model   string
	client  *openai.Client
}

// NewSearchTermClient builds a SearchTermClient. model may be empty.
func NewSearchTermClient(baseURL, model string, client *http.Client) *SearchTermClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &SearchTermClient{
		baseURL: baseURL,
		model:   model,
		client:  openai.NewClientWithConfig(openai.ClientConfig{BaseURL: baseURL, HTTPClient: client}),
	}
}

const searchTermPrompt = "Return a search query in the form of a question, from the following prompt:"

// ExtractSearchTerm asks the model for a search query phrased as a question.
// An empty prompt yields an empty term without calling the model.
func (c *SearchTermClient) ExtractSearchTerm(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", nil
	}

	completion, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:           c.model,
		ReasoningEffort: "off",
		Messages: []openai.ChatCompletionMessage{{
			Role:    "user",
			Content: searchTermPrompt + "\n\n" + prompt,
		}},
	})
	if err != nil {
		return "", err
	}
	if len(completion.Choices) == 0 {
		return "", errors.New("search-term model returned no choices")
	}

	return CleanSearchTerm(completion.Choices[0].Message.Content), nil
}

// CleanSearchTerm strips code fences and surrounding quotes from a model reply.
func CleanSearchTerm(term string) string {
	term = strings.TrimSpace(term)
	term = strings.TrimPrefix(term, "```json\n")
	term = strings.TrimSuffix(term, "```")
	return strings.Trim(strings.TrimSpace(term), `"`)
}
