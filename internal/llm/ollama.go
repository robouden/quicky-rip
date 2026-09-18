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

// Ollama is a Client backed by a local Ollama server's OpenAI-compatible
// /v1/chat/completions endpoint (supports forced tool calls since Ollama
// 0.3+). BaseURL has no trailing slash, e.g. "http://localhost:11434".
type Ollama struct {
	BaseURL string
	HC      *http.Client
}

func NewOllama(baseURL string) *Ollama {
	return &Ollama{BaseURL: baseURL, HC: &http.Client{Timeout: 180 * time.Second}}
}

type oaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type oaTool struct {
	Type     string     `json:"type"`
	Function oaFunction `json:"function"`
}

type oaToolChoice struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

type oaRequest struct {
	Model      string      `json:"model"`
	Messages   []oaMessage `json:"messages"`
	Tools      []oaTool    `json:"tools,omitempty"`
	ToolChoice any         `json:"tool_choice,omitempty"`
	Stream     bool        `json:"stream"`
}

type oaToolCall struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaResponse struct {
	Choices []struct {
		Message struct {
			Content   string       `json:"content"`
			ToolCalls []oaToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (o *Ollama) messages(req Request) []oaMessage {
	var msgs []oaMessage
	if req.System != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, oaMessage{Role: "user", Content: req.User})
	return msgs
}

func (o *Ollama) Text(ctx context.Context, req Request) (string, error) {
	resp, err := o.do(ctx, oaRequest{Model: req.Model, Messages: o.messages(req)})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm: ollama returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func (o *Ollama) Tool(ctx context.Context, req Request, tool Tool, out any) error {
	body := oaRequest{
		Model:    req.Model,
		Messages: o.messages(req),
		Tools: []oaTool{{
			Type: "function",
			Function: oaFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		}},
	}
	choice := oaToolChoice{Type: "function"}
	choice.Function.Name = tool.Name
	body.ToolChoice = choice

	resp, err := o.do(ctx, body)
	if err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return ErrNoToolCall
	}
	for _, tc := range resp.Choices[0].Message.ToolCalls {
		if tc.Function.Name == tool.Name {
			return json.Unmarshal([]byte(tc.Function.Arguments), out)
		}
	}
	return ErrNoToolCall
}

func (o *Ollama) do(ctx context.Context, body oaRequest) (*oaResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")

	res, err := o.HC.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var out oaResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("llm: ollama: %s: %s", res.Status, raw)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("llm: ollama: %s", out.Error.Message)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: ollama: %s: %s", res.Status, raw)
	}
	return &out, nil
}
