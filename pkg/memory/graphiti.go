package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GraphitiClient queries the Graphiti memory graph.
type GraphitiClient struct {
	baseURL  string
	maxFacts int
	http     *http.Client
}

// NewGraphitiClient builds a GraphitiClient.
func NewGraphitiClient(baseURL string, maxFacts int, client *http.Client) *GraphitiClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &GraphitiClient{baseURL: baseURL, maxFacts: maxFacts, http: client}
}

type searchRequest struct {
	Query    string `json:"query"`
	MaxFacts int    `json:"max_facts"`
}

// Search runs a graph query and returns the raw JSON response body.
func (c *GraphitiClient) Search(ctx context.Context, query string) (string, error) {
	body, err := json.Marshal(searchRequest{Query: query, MaxFacts: c.maxFacts})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/graphiti/search", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("memory search returned %s: %s", resp.Status, raw)
	}
	return string(raw), nil
}

// FormatFacts renders a search response as one "- fact" line per fact,
// falling back to the trimmed raw body when it is not the expected shape.
func FormatFacts(raw string) string {
	var response struct {
		Facts []struct {
			Fact string `json:"fact"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err == nil && len(response.Facts) > 0 {
		facts := make([]string, 0, len(response.Facts))
		for _, fact := range response.Facts {
			if fact := strings.TrimSpace(fact.Fact); fact != "" {
				facts = append(facts, "- "+fact)
			}
		}
		if len(facts) > 0 {
			return strings.Join(facts, "\n")
		}
	}
	return strings.TrimSpace(raw)
}
