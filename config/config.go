package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// QdrantConfig holds gRPC connection settings for the Qdrant vector database.
type QdrantConfig struct {
	Host   string `mapstructure:"host"    yaml:"host"`
	Port   int    `mapstructure:"port"    yaml:"port"`
	UseTLS bool   `mapstructure:"use_tls" yaml:"use_tls"`
}

// LLMConfig holds settings for the language model provider.
type LLMConfig struct {
	Model          string   `mapstructure:"model"           yaml:"model"`
	FallbackModels []string `mapstructure:"fallback_models" yaml:"fallback_models"`
	BatchSize      int      `mapstructure:"batch_size"      yaml:"batch_size"`
	// ReadmeModel is the model used to generate the project README. It defaults
	// to qwen/qwen3-coder but can be overridden in config.yaml.
	ReadmeModel string `mapstructure:"readme_model" yaml:"readme_model"`
	// RequestTimeout bounds an individual summarise/embed request, in seconds.
	// ReadmeTimeout bounds README generation, which sends the whole project at
	// once and so needs a much larger budget. Defaults: 60s and 300s.
	RequestTimeout int `mapstructure:"request_timeout" yaml:"request_timeout"`
	ReadmeTimeout  int `mapstructure:"readme_timeout"  yaml:"readme_timeout"`
}

// EmbeddingConfig holds settings for the OpenRouter embeddings endpoint.
type EmbeddingConfig struct {
	Model     string `mapstructure:"model"      yaml:"model"`
	Dimension int    `mapstructure:"dimension"  yaml:"dimension"`
	// QueryInstruction is the task instruction prepended to search queries (but
	// not to indexed documents) for instruction-aware asymmetric embedders such
	// as Qwen3-Embedding. Leave empty to embed queries verbatim.
	QueryInstruction string `mapstructure:"query_instruction" yaml:"query_instruction"`
	// MaxTokens is the embedding model's maximum input length in tokens. Inputs
	// longer than this are rejected by the API, so oversized symbols are
	// truncated to fit before embedding. Defaults to 8192.
	MaxTokens int `mapstructure:"max_tokens" yaml:"max_tokens"`
}

// IndexingConfig holds settings that control the indexing pipeline.
type IndexingConfig struct {
	Concurrency  int      `mapstructure:"concurrency"   yaml:"concurrency"`
	SkipPatterns []string `mapstructure:"skip_patterns" yaml:"skip_patterns"`
	MinLines     int      `mapstructure:"min_lines"     yaml:"min_lines"`
}

// HooksConfig holds settings used by git and editor hooks.
type HooksConfig struct {
	GitSkipEnv      string  `mapstructure:"git_skip_env"    yaml:"git_skip_env"`
	ClaudeTopK      int     `mapstructure:"claude_top_k"    yaml:"claude_top_k"`
	ClaudeMinScore  float64 `mapstructure:"claude_min_score" yaml:"claude_min_score"`
	ClaudeMaxTokens int     `mapstructure:"claude_max_tokens" yaml:"claude_max_tokens"`
}

// Config is the root configuration structure.
type Config struct {
	Qdrant    QdrantConfig    `mapstructure:"qdrant"     yaml:"qdrant"`
	LLM       LLMConfig       `mapstructure:"llm"        yaml:"llm"`
	Embedding EmbeddingConfig `mapstructure:"embedding"  yaml:"embedding"`
	Indexing  IndexingConfig  `mapstructure:"indexing"   yaml:"indexing"`
	Hooks     HooksConfig     `mapstructure:"hooks"      yaml:"hooks"`
}

// ProjectConfig holds per-project overrides stored in ~/.nav-cli/projects/<name>.yaml.
type ProjectConfig struct {
	Name       string         `mapstructure:"name"       yaml:"name"`
	RootPath   string         `mapstructure:"root_path"  yaml:"root_path"`
	Collection string         `mapstructure:"collection" yaml:"collection"`
	Indexing   IndexingConfig `mapstructure:"indexing"   yaml:"indexing,omitempty"`
	Hooks      HooksConfig    `mapstructure:"hooks"      yaml:"hooks,omitempty"`
}

// Credentials holds API keys loaded from ~/.nav-cli/credentials.
type Credentials struct {
	OpenRouterAPIKey string
	QdrantAPIKey     string
}

// Dir returns the path to the nav-cli config directory.
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".nav-cli")
	}
	return filepath.Join(home, ".nav-cli")
}

// Load reads ~/.nav-cli/config.yaml using viper, applying built-in defaults for
// any keys that are absent from the file.
func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("qdrant.host", "127.0.0.1")
	v.SetDefault("qdrant.port", 6334)
	v.SetDefault("qdrant.use_tls", false)

	v.SetDefault("llm.model", "qwen/qwen3-coder")
	v.SetDefault("llm.fallback_models", []string{
		"mistralai/devstral-2",
		"meta-llama/llama-3.3-70b-instruct",
	})
	v.SetDefault("llm.batch_size", 10)
	v.SetDefault("llm.readme_model", "qwen/qwen3-coder")
	v.SetDefault("llm.request_timeout", 60)
	v.SetDefault("llm.readme_timeout", 300)

	v.SetDefault("embedding.model", "qwen/qwen3-embedding-8b")
	v.SetDefault("embedding.dimension", 4096)
	v.SetDefault("embedding.query_instruction", "Given a code search query, retrieve relevant code symbols that satisfy it")
	v.SetDefault("embedding.max_tokens", 8192)

	v.SetDefault("indexing.concurrency", 4)
	v.SetDefault("indexing.skip_patterns", []string{
		"vendor/**",
		"node_modules/**",
		"**/*_test.go",
		"**/*.pb.go",
		"dist/**",
		"venv/**",
		".venv/**",
		"env/**",
		".env/**",
		"virtualenv/**",
		"**/site-packages/**",
		"**/__pycache__/**",
	})
	v.SetDefault("indexing.min_lines", 3)

	v.SetDefault("hooks.git_skip_env", "NAV_SKIP")
	v.SetDefault("hooks.claude_top_k", 5)
	v.SetDefault("hooks.claude_min_score", 0.72)
	v.SetDefault("hooks.claude_max_tokens", 4000)

	// Config file location
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(Dir())

	if err := v.ReadInConfig(); err != nil {
		// It is acceptable for the config file to be absent; defaults apply.
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}
	return &cfg, nil
}

// LoadCredentials reads ~/.nav-cli/credentials which is a KEY=VALUE file,
// one entry per line. Lines starting with '#' and blank lines are ignored.
func LoadCredentials() (*Credentials, error) {
	path := filepath.Join(Dir(), "credentials")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Credentials{}, nil
		}
		return nil, fmt.Errorf("opening credentials: %w", err)
	}
	defer f.Close()

	creds := &Credentials{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "OPENROUTER_API_KEY":
			creds.OpenRouterAPIKey = val
		case "QDRANT_API_KEY":
			creds.QdrantAPIKey = val
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}
	return creds, nil
}

// SaveCredentials writes the credentials to ~/.nav-cli/credentials with 0600
// permissions so that the file is readable only by the owning user.
func SaveCredentials(creds *Credentials) error {
	if err := EnsureDir(); err != nil {
		return err
	}
	path := filepath.Join(Dir(), "credentials")

	var sb strings.Builder
	writeKV := func(key, val string) {
		if val != "" {
			fmt.Fprintf(&sb, "%s=%s\n", key, val)
		}
	}
	writeKV("OPENROUTER_API_KEY", creds.OpenRouterAPIKey)
	writeKV("QDRANT_API_KEY", creds.QdrantAPIKey)

	if err := os.WriteFile(path, []byte(sb.String()), 0600); err != nil {
		return fmt.Errorf("writing credentials: %w", err)
	}
	return nil
}

// EnsureDir creates the nav-cli directory hierarchy if it does not already exist.
func EnsureDir() error {
	base := Dir()
	for _, sub := range []string{base, filepath.Join(base, "projects"), filepath.Join(base, "logs")} {
		if err := os.MkdirAll(sub, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", sub, err)
		}
	}
	return nil
}

// WriteDefault writes a default config.yaml to ~/.nav-cli/ only when the file
// does not already exist. It marshals a Config populated entirely with defaults.
func WriteDefault() error {
	if err := EnsureDir(); err != nil {
		return err
	}
	path := filepath.Join(Dir(), "config.yaml")
	if _, err := os.Stat(path); err == nil {
		// File already exists — do not overwrite.
		return nil
	}

	cfg := Config{
		Qdrant: QdrantConfig{
			Host: "localhost",
			Port: 6334,
		},
		LLM: LLMConfig{
			Model: "qwen/qwen3-coder",
			FallbackModels: []string{
				"mistralai/devstral-2",
				"meta-llama/llama-3.3-70b-instruct",
			},
			BatchSize:      10,
			ReadmeModel:    "qwen/qwen3-coder",
			RequestTimeout: 60,
			ReadmeTimeout:  300,
		},
		Embedding: EmbeddingConfig{
			Model:            "qwen/qwen3-embedding-8b",
			Dimension:        4096,
			QueryInstruction: "Given a code search query, retrieve relevant code symbols that satisfy it",
			MaxTokens:        8192,
		},
		Indexing: IndexingConfig{
			Concurrency: 4,
			SkipPatterns: []string{
				"vendor/**",
				"node_modules/**",
				"**/*_test.go",
				"**/*.pb.go",
				"dist/**",
				"venv/**",
				".venv/**",
				"env/**",
				".env/**",
				"virtualenv/**",
				"**/site-packages/**",
				"**/__pycache__/**",
			},
			MinLines: 3,
		},
		Hooks: HooksConfig{
			GitSkipEnv:      "NAV_SKIP",
			ClaudeTopK:      5,
			ClaudeMinScore:  0.4,
			ClaudeMaxTokens: 4000,
		},
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling default config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing default config: %w", err)
	}
	return nil
}
