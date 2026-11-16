// Package laya talks to a laya-router "systemone" decision endpoint.
package laya

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client asks a laya-router for structured decisions over a piece of state.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// New builds a Client. An empty apiKey disables the client (Enabled is false).
func New(baseURL, apiKey, model string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: baseURL, apiKey: apiKey, model: model, http: client}
}

// Enabled reports whether the client has an API key.
func (c *Client) Enabled() bool { return c.apiKey != "" }

type question struct {
	Type         string         `json:"type"`
	Instructions string         `json:"instructions"`
	Criteria     map[string]any `json:"criteria"`
}

type request struct {
	Model     string              `json:"model"`
	State     string              `json:"state"`
	Questions map[string]question `json:"questions"`
}

type decision struct {
	Choice string `json:"choice"`
}

type response struct {
	Answers map[string]decision `json:"answers"`
}

// decisionQuestion is the (only) question id this client posts.
const decisionQuestion = "decision"

// Decide asks the router a single choice question over state and returns the
// chosen criterion name. A missing answer yields "".
func (c *Client) Decide(ctx context.Context, state, instructions string, criteria map[string]any) (string, error) {
	body, err := json.Marshal(request{
		Model: c.model,
		State: state,
		Questions: map[string]question{
			decisionQuestion: {
				Type:         "choice",
				Instructions: instructions,
				Criteria:     criteria,
			},
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("laya decision returned %s: %s", resp.Status, responseBody)
	}

	var parsed response
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return "", fmt.Errorf("laya decision response is not JSON: %w", err)
	}
	return parsed.Answers[decisionQuestion].Choice, nil
}
