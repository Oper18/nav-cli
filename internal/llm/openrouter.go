package llm

// ChatRequest is the request body sent to the OpenRouter chat completions API.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
}

// ChatMessage is a single message in a chat conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse is the response body from the OpenRouter chat completions API.
type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// SummariseRequest is the input to the LLM summarisation step.
type SummariseRequest struct {
	Language string
	Symbol   string
	Type     string
	Content  string
	// ProjectContext is the project-level README, supplied so each symbol summary
	// can be grounded in the project's overall purpose. It may be empty (e.g. on
	// the first index before a README exists).
	ProjectContext string
}

// SummariseResponse is what the LLM returns for a single symbol.
type SummariseResponse struct {
	// Summary is a dense, up-to-200-character description of what the code does.
	Summary string
	Tags    []string
	// BusinessContext explains the business/domain purpose the symbol serves
	// rather than its implementation.
	BusinessContext string
	// Responsibilities is a short list of the distinct responsibilities the
	// symbol owns.
	Responsibilities []string
}

// ReadmeRequest is the input to the project-level README generation step. The
// README is generated from the project's source up front (before per-symbol
// summarisation), so it carries the source as evidence rather than summaries.
type ReadmeRequest struct {
	Project   string
	Languages []string
	// Source is a budgeted concatenation of the project's indexed code, used as
	// evidence of what the project does.
	Source string
}
