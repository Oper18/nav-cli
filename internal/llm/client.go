package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Default per-operation deadlines, used when NewClient is given a non-positive
// value. README generation sends the whole project at once and streams a long
// document, so it gets a much larger budget than a single-symbol summary.
const (
	defaultRequestTimeout = 60 * time.Second
	defaultReadmeTimeout  = 5 * time.Minute
)

// Client is an OpenRouter HTTP client with retry/fallback logic.
type Client struct {
	apiKey         string
	primaryModel   string
	fallbackModels []string
	httpClient     *http.Client
	baseURL        string
	requestTimeout time.Duration
	embedTimeout   time.Duration
	readmeTimeout  time.Duration
}

// NewClientWithEmbedTimeout creates a new Client using the given API key and models.
// primaryModel is tried first; fallbackModels are tried in order on failure.
// requestTimeout bounds individual summarise calls and embedTimeout bounds embed calls;
// readmeTimeout bounds README generation; non-positive values fall back to the defaults.
func NewClientWithEmbedTimeout(apiKey, primaryModel string, fallbackModels []string, requestTimeout, embedTimeout, readmeTimeout time.Duration) *Client {
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}
	if embedTimeout <= 0 {
		embedTimeout = defaultRequestTimeout * 2 // Default embed timeout to be double of request timeout
	}
	if readmeTimeout <= 0 {
		readmeTimeout = defaultReadmeTimeout
	}
	return &Client{
		apiKey:         apiKey,
		primaryModel:   primaryModel,
		fallbackModels: fallbackModels,
		// No client-level Timeout: each operation applies its own deadline via the
		// request context, so a long README generation is not cut short by the
		// tight budget appropriate for per-symbol summarisation.
		httpClient:     &http.Client{},
		baseURL:        "https://openrouter.ai/api/v1",
		requestTimeout: requestTimeout,
		embedTimeout:   embedTimeout,
		readmeTimeout:  readmeTimeout,
	}
}

// NewClient creates a new Client using the given API key and models.
// This is a wrapper for backward compatibility keeping the original interface.
// primaryModel is tried first; fallbackModels are tried in order on a 429 (rate-limit) or any other non-2xx response.
// requestTimeout bounds individual summarise/embed calls and readmeTimeout
// bounds README generation; non-positive values fall back to the defaults.
func NewClient(apiKey, primaryModel string, fallbackModels []string, requestTimeout, readmeTimeout time.Duration) *Client {
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}
	if readmeTimeout <= 0 {
		readmeTimeout = defaultReadmeTimeout
	}
	// For backward compatibility, embedTimeout is set the same as requestTimeout
	return &Client{
		apiKey:         apiKey,
		primaryModel:   primaryModel,
		fallbackModels: fallbackModels,
		// No client-level Timeout: each operation applies its own deadline via the
		// request context, so a long README generation is not cut short by the
		// tight budget appropriate for per-symbol summarisation.
		httpClient:     &http.Client{},
		baseURL:        "https://openrouter.ai/api/v1",
		requestTimeout: requestTimeout,
		embedTimeout:   requestTimeout, // Same as request timeout for backward compatibility
		readmeTimeout:  readmeTimeout,
	}
}

// Summarise calls the OpenRouter chat completions API to get a summary and tags
// for a code symbol. It tries primaryModel first, then each fallback model in
// order on a 429 (rate-limit) or any other non-2xx response.
func (c *Client) Summarise(ctx context.Context, req SummariseRequest) (SummariseResponse, error) {
	models := make([]string, 0, 1+len(c.fallbackModels))
	models = append(models, c.primaryModel)
	models = append(models, c.fallbackModels...)

	prompt := buildSummarisePrompt(req)

	var lastErr error
	allRateLimited := true

	for _, model := range models {
		attemptCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
		resp, rateLimited, err := c.callModel(attemptCtx, model, prompt)
		cancel()
		if err == nil {
			return resp, nil
		}
		if !rateLimited {
			allRateLimited = false
		}
		lastErr = err
	}

	if allRateLimited {
		return SummariseResponse{}, errors.New("all models rate-limited")
	}
	return SummariseResponse{}, lastErr
}

// callModel sends a single request to the given model and returns the parsed
// SummariseResponse. The second return value is true when the server responded
// with HTTP 429 (rate-limited).
func (c *Client) callModel(ctx context.Context, model, prompt string) (SummariseResponse, bool, error) {
	chatReq := ChatRequest{
		Model: model,
		Messages: []ChatMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return SummariseResponse{}, false, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return SummariseResponse{}, false, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return SummariseResponse{}, false, fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusTooManyRequests {
		return SummariseResponse{}, true, fmt.Errorf("model %s: rate limited (429)", model)
	}

	rawBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return SummariseResponse{}, false, fmt.Errorf("read response body: %w", err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return SummariseResponse{}, false,
			fmt.Errorf("model %s: non-2xx status %d: %s", model, httpResp.StatusCode, rawBody)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(rawBody, &chatResp); err != nil {
		return SummariseResponse{}, false, fmt.Errorf("unmarshal response: %w", err)
	}

	if chatResp.Error != nil {
		return SummariseResponse{}, false,
			fmt.Errorf("model %s: API error (code %d): %s", model, chatResp.Error.Code, chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return SummariseResponse{}, false, fmt.Errorf("model %s: no choices in response", model)
	}

	content := chatResp.Choices[0].Message.Content

	// Try to parse the assistant reply as the structured summary object.
	var parsed struct {
		Summary          string   `json:"summary"`
		Tags             []string `json:"tags"`
		BusinessContext  string   `json:"businessContext"`
		Responsibilities []string `json:"responsibilities"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// Fallback: treat the whole content as the summary with no extra fields.
		return SummariseResponse{Summary: content}, false, nil
	}

	return SummariseResponse{
		Summary:          parsed.Summary,
		Tags:             parsed.Tags,
		BusinessContext:  parsed.BusinessContext,
		Responsibilities: parsed.Responsibilities,
	}, false, nil
}

// GenerateReadme asks the LLM to produce a project-level README focused on
// business logic and high-level technical decisions. It tries the given model
// first (falling back to the client's primary model when empty), then each
// fallback model in order on failure, and returns the raw Markdown document.
func (c *Client) GenerateReadme(ctx context.Context, model string, req ReadmeRequest) (string, error) {
	if model == "" {
		model = c.primaryModel
	}
	models := make([]string, 0, 1+len(c.fallbackModels))
	models = append(models, model)
	models = append(models, c.fallbackModels...)

	prompt := buildReadmePrompt(req)

	var lastErr error
	for _, model := range models {
		attemptCtx, cancel := context.WithTimeout(ctx, c.readmeTimeout)
		resp, _, err := c.callModel(attemptCtx, model, prompt)
		cancel()
		if err == nil {
			return resp.Summary, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no model produced a README")
	}
	return "", lastErr
}

// SummariseBatch processes multiple SummariseRequests concurrently using a
// semaphore of size concurrency. Results are returned in the same order as
// inputs. The returned slices are always len(reqs); individual errors are
// stored in the error slice.
func (c *Client) SummariseBatch(ctx context.Context, reqs []SummariseRequest, concurrency int) ([]SummariseResponse, []error) {
	results := make([]SummariseResponse, len(reqs))
	errs := make([]error, len(reqs))

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, req := range reqs {
		wg.Add(1)
		sem <- struct{}{} // acquire
		go func(idx int, r SummariseRequest) {
			defer wg.Done()
			defer func() { <-sem }() // release
			resp, err := c.Summarise(ctx, r)
			results[idx] = resp
			errs[idx] = err
		}(i, req)
	}

	wg.Wait()
	return results, errs
}
