package llm

import (
	"fmt"
	"strings"
)

// buildSummarisePrompt returns the prompt sent to the LLM for code summarisation.
// It is used internally by client.go.
func buildSummarisePrompt(req SummariseRequest) string {
	return fmt.Sprintf(
		"You are a code documentation assistant.\n"+
			"Given the source code below, respond ONLY with a JSON object containing exactly these fields:\n"+
			"  \"summary\": a dense description of what this code does, up to 200 characters. "+
			"Go beyond a one-liner: cover the inputs it consumes, the work it performs and what it returns or mutates.\n"+
			"  \"tags\": an array of 3-6 lowercase keywords\n"+
			"  \"businessContext\": one sentence describing the business/domain purpose this code serves (the why), not its implementation\n"+
			"  \"responsibilities\": an array of 1-4 short phrases, each naming one distinct responsibility this code owns\n\n"+
			"Language: %s\n"+
			"Symbol: %s\n"+
			"Type: %s\n\n"+
			"Source:\n%s",
		req.Language,
		req.Symbol,
		req.Type,
		req.Content,
	)
}

// buildReadmePrompt returns the prompt that asks the LLM to write a project-level
// README focused on business logic and high-level technical decisions. The
// resulting document must deliberately avoid code or implementation detail.
func buildReadmePrompt(req ReadmeRequest) string {
	var b strings.Builder
	for _, s := range req.Symbols {
		ctx := s.BusinessContext
		if ctx == "" {
			ctx = s.Summary
		}
		fmt.Fprintf(&b, "- %s (%s, %s): %s\n", s.Symbol, s.Type, s.FilePath, ctx)
	}

	return fmt.Sprintf(
		"You are a technical writer producing the README for the %q project.\n\n"+
			"Write the README in Markdown. It MUST describe the business logic — what the "+
			"project is for, the domain problems it solves, its main capabilities and the "+
			"workflows it supports. You MAY add a short, high-level note on the technical "+
			"stack and a couple of notable architecture decisions.\n\n"+
			"Strict rules:\n"+
			"  - Do NOT include any code, code blocks, function signatures or file paths.\n"+
			"  - Do NOT describe low-level implementation details or individual functions.\n"+
			"  - Stay high level: a stakeholder who cannot read code should understand it.\n"+
			"  - Respond ONLY with the Markdown document, no preamble.\n\n"+
			"Detected languages: %s\n\n"+
			"The following per-symbol notes are provided only as evidence of what the "+
			"project does — synthesise them into business capabilities, do not list them "+
			"verbatim:\n%s",
		req.Project,
		strings.Join(req.Languages, ", "),
		b.String(),
	)
}
