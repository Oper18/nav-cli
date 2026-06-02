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

// ReadmeSymbol is a compact, code-free descriptor of one indexed symbol that is
// fed to the project-README generator.
type ReadmeSymbol struct {
	Symbol          string
	FilePath        string
	Type            string
	Summary         string
	BusinessContext string
}

// ReadmeRequest is the input to the project-level README generation step.
type ReadmeRequest struct {
	Project   string
	Languages []string
	Symbols   []ReadmeSymbol
}
