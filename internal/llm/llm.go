// Package llm is a thin Anthropic Messages client. It is an interface so the
// pipeline stages are testable without network access.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client interface {
	// Text returns the concatenated text blocks of the response.
	Text(ctx context.Context, req Request) (string, error)
	// Tool runs a forced tool call and unmarshals its input into out.
	Tool(ctx context.Context, req Request, tool Tool, out any) error
}

type Request struct {
	Model     string
	MaxTokens int
	// System is sent as a content block so CacheSystem can mark it ephemeral.
	System      string
	CacheSystem bool
	User        string
}

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type HTTP struct {
	APIKey  string
	BaseURL string
	HC      *http.Client
}

func New(apiKey string) *HTTP {
	return &HTTP{APIKey: apiKey, BaseURL: "https://api.anthropic.com", HC: &http.Client{Timeout: 120 * time.Second}}
}

type contentBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type apiRequest struct {
	Model      string         `json:"model"`
	MaxTokens  int            `json:"max_tokens"`
	System     []contentBlock `json:"system,omitempty"`
	Messages   []apiMessage   `json:"messages"`
	Tools      []apiTool      `json:"tools,omitempty"`
	ToolChoice *apiToolChoice `json:"tool_choice,omitempty"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type apiToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type apiResponse struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
}

func (c *HTTP) build(req Request) apiRequest {
	out := apiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  []apiMessage{{Role: "user", Content: req.User}},
	}
	if req.System != "" {
		b := contentBlock{Type: "text", Text: req.System}
		if req.CacheSystem {
			b.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		out.System = []contentBlock{b}
	}
	return out
}

func (c *HTTP) Text(ctx context.Context, req Request) (string, error) {
	resp, err := c.do(ctx, c.build(req))
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	for _, b := range resp.Content {
		if b.Type == "text" {
			buf.WriteString(b.Text)
		}
	}
	return buf.String(), nil
}

// ErrNoToolCall means the model answered without invoking the forced tool.
// Callers that have a structural retry (synthesis site 2) key off this.
var ErrNoToolCall = fmt.Errorf("llm: response contained no tool_use block")

func (c *HTTP) Tool(ctx context.Context, req Request, tool Tool, out any) error {
	body := c.build(req)
	body.Tools = []apiTool{{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}}
	body.ToolChoice = &apiToolChoice{Type: "tool", Name: tool.Name}
	resp, err := c.do(ctx, body)
	if err != nil {
		return err
	}
	for _, b := range resp.Content {
		if b.Type == "tool_use" && b.Name == tool.Name {
			return json.Unmarshal(b.Input, out)
		}
	}
	return ErrNoToolCall
}

func (c *HTTP) do(ctx context.Context, body apiRequest) (*apiResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	res, err := c.HC.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: %s: %s", res.Status, raw)
	}
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
