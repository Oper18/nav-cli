package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// embedRequest is the OpenRouter embeddings request body.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the OpenRouter embeddings response body.
type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// EmbedQuery embeds search queries using the model's instruction-aware query
// format. Asymmetric embedders such as Qwen3-Embedding expect queries to carry
// a task instruction prefix while documents are embedded bare; applying the
// prefix only on the query side keeps query and document vectors in the space
// the model was trained for. When instruction is empty the queries are embedded
// unmodified.
func (c *Client) EmbedQuery(ctx context.Context, model, instruction string, queries []string) ([][]float32, error) {
	if instruction == "" {
		return c.Embed(ctx, model, queries)
	}
	formatted := make([]string, len(queries))
	for i, q := range queries {
		formatted[i] = fmt.Sprintf("Instruct: %s\nQuery: %s", instruction, q)
	}
	return c.Embed(ctx, model, formatted)
}

// Embed sends texts to OpenRouter's embeddings endpoint using the given model
// and returns vectors in input order.
func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	body, err := json.Marshal(embedRequest{Model: model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embed response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embed: status %d: %s", resp.StatusCode, raw)
	}

	var parsed embedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("embed: API error (code %d): %s", parsed.Error.Code, parsed.Error.Message)
	}

	// Preserve input order.
	sort.Slice(parsed.Data, func(i, j int) bool {
		return parsed.Data[i].Index < parsed.Data[j].Index
	})

	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = d.Embedding
	}
	return out, nil
}
