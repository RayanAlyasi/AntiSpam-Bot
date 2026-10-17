package ai

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

// Providers with built-in adapters. The empty provider means the AI is off: the UNCERTAIN band
// goes straight to admins (DESIGN.md §7.4, "fail safe").
const (
	ProviderNone             = ""
	ProviderAnthropic        = "anthropic"
	ProviderOpenAICompatible = "openai_compatible" // hosted services and Ollama, llama.cpp, vLLM, LM Studio
	ProviderGoogle           = "google"
)

// OutputSetting is the structured_output setting.
type OutputSetting string

// Structured-output settings.
const (
	OutputAuto   OutputSetting = "auto"   // native schema when the provider supports it, else JSON
	OutputNative OutputSetting = "native" // provider-enforced JSON schema or tool output
	OutputJSON   OutputSetting = "json"   // JSON mode plus strict parsing and validation
)

// Config is the ai: section of the configuration. The key itself never appears in a file:
// APIKeyEnv names the environment variable that holds it.
type Config struct {
	Provider         string        `yaml:"provider"`
	BaseURL          string        `yaml:"base_url"` // required for openai_compatible
	Model            string        `yaml:"model"`
	APIKeyEnv        string        `yaml:"api_key_env"` // empty for most local servers
	Timeout          time.Duration `yaml:"timeout"`
	StructuredOutput OutputSetting `yaml:"structured_output"`
	MaxOutputTokens  int           `yaml:"max_output_tokens"`
	MaxInputChars    int           `yaml:"max_input_chars"` // cap on the rendered data block
}

// Defaults. Local models are slower: set a longer timeout for them (e.g. 20s).
const (
	DefaultTimeout         = 5 * time.Second
	DefaultMaxOutputTokens = 256
	DefaultMaxInputChars   = 6000
	maxTimeout             = 10 * time.Minute
)

var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// WithDefaults fills in zero values.
func (c Config) WithDefaults() Config {
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.StructuredOutput == "" {
		c.StructuredOutput = OutputAuto
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if c.MaxInputChars == 0 {
		c.MaxInputChars = DefaultMaxInputChars
	}
	return c
}

// Enabled reports whether an AI provider is configured.
func (c Config) Enabled() bool { return c.Provider != ProviderNone }

// Validate checks a configuration after WithDefaults. A disabled configuration is always valid.
func (c Config) Validate() error {
	switch c.Provider {
	case ProviderNone:
		return nil
	case ProviderAnthropic, ProviderOpenAICompatible, ProviderGoogle:
	default:
		return fmt.Errorf("ai.provider %q: want anthropic, openai_compatible or google", c.Provider)
	}
	if c.Model == "" {
		return errors.New("ai.model is required")
	}
	if c.Provider == ProviderOpenAICompatible && c.BaseURL == "" {
		return errors.New("ai.base_url is required for openai_compatible")
	}
	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("ai.base_url %q: want an http or https URL", c.BaseURL)
		}
		if u.User != nil {
			return errors.New("ai.base_url must not contain credentials; use ai.api_key_env")
		}
	}
	if c.APIKeyEnv != "" && !envName.MatchString(c.APIKeyEnv) {
		return errors.New("ai.api_key_env must be the name of an environment variable (e.g. AI_API_KEY), not the key")
	}
	if c.Timeout <= 0 || c.Timeout > maxTimeout {
		return fmt.Errorf("ai.timeout %v: want more than 0 and at most %v", c.Timeout, maxTimeout)
	}
	switch c.StructuredOutput {
	case OutputAuto, OutputNative, OutputJSON:
	default:
		return fmt.Errorf("ai.structured_output %q: want auto, native or json", c.StructuredOutput)
	}
	if c.MaxOutputTokens <= 0 || c.MaxInputChars <= 0 {
		return errors.New("ai.max_output_tokens and ai.max_input_chars must be positive")
	}
	return nil
}

// APIKey reads the key from the environment variable APIKeyEnv names ("" when unset).
func (c Config) APIKey(getenv func(string) string) string {
	if c.APIKeyEnv == "" {
		return ""
	}
	return getenv(c.APIKeyEnv)
}
