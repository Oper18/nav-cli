package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

// embedRateLimitDelay is how long Embed waits before retrying after the
// embeddings endpoint responds with HTTP 429 (rate limit exceeded). Free-tier
// OpenRouter rate limits reset per minute, so this comfortably clears the
// window before the next attempt.
const embedRateLimitDelay = 2 * time.Minute

// embedRequest is the OpenRouter embeddings request body.
type embedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
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
// and returns vectors in input order. On a 429 (rate limit exceeded) response
// it waits out embedRateLimitDelay and retries indefinitely, since indexing
// runs can be long-lived and the free-tier per-minute limit is transient; a
// cancelled or deadline-exceeded ctx still aborts the wait immediately.
func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: model, Input: texts, Dimensions: c.embedDimensions})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	for {
		out, rateLimited, err := c.doEmbed(ctx, body)
		if !rateLimited {
			return out, err
		}

		fmt.Fprintf(os.Stderr, "\nembed: rate limited (429), waiting %s before retrying...\n", embedRateLimitDelay)
		select {
		case <-time.After(embedRateLimitDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// doEmbed performs a single embeddings request attempt, bounded by
// c.embedTimeout. The second return value is true when the response was a
// 429 (rate limited), signalling to Embed that the request should be retried
// rather than treated as a terminal failure.
func (c *Client) doEmbed(ctx context.Context, body []byte) ([][]float32, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.embedTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("read embed response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, true, fmt.Errorf("embed: status %d: %s", resp.StatusCode, raw)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("embed: status %d: %s", resp.StatusCode, raw)
	}

	var parsed embedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, false, fmt.Errorf("decode embed response: %w", err)
	}
	if parsed.Error != nil {
		rateLimited := parsed.Error.Code == http.StatusTooManyRequests
		return nil, rateLimited, fmt.Errorf("embed: API error (code %d): %s", parsed.Error.Code, parsed.Error.Message)
	}

	// Preserve input order.
	sort.Slice(parsed.Data, func(i, j int) bool {
		return parsed.Data[i].Index < parsed.Data[j].Index
	})

	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = d.Embedding
	}
	return out, false, nil
}
