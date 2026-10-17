package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
)

// repoRoot is the repository root, seen from this package's directory.
const repoRoot = "../.."

// getenvLiteral finds environment variables read by name: getenv("X"), os.Getenv("X") and
// os.LookupEnv("X").
var getenvLiteral = regexp.MustCompile(`(?:[gG]etenv|LookupEnv)\("([A-Z][A-Z0-9_]*)"\)`)

// readEnvVars returns every environment variable the bot's code reads by literal name.
func readEnvVars(t *testing.T) map[string]string {
	t.Helper()
	vars := map[string]string{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range getenvLiteral.FindAllStringSubmatch(string(src), -1) {
				vars[m[1]] = path
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return vars
}

// documentedEnvVars returns the variables env.example documents, set or commented out
// ("NAME=value" or "# NAME=value" at the start of a line).
func documentedEnvVars(t *testing.T) map[string]bool {
	t.Helper()
	example, err := os.ReadFile(filepath.Join(repoRoot, "env.example"))
	if err != nil {
		t.Fatalf("read env.example: %v (deployers copy it to .env, DESIGN §19.1)", err)
	}
	assignment := regexp.MustCompile(`^(?:#\s*)?([A-Z][A-Z0-9_]*)=`)
	documented := map[string]bool{}
	for line := range strings.SplitSeq(string(example), "\n") {
		if m := assignment.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			documented[m[1]] = true
		}
	}
	return documented
}

// TestConfigReadsOnlyDocumentedVariables records every name config.Load and healthcheck ask
// for, so reads through constants, tables or a configured name (ai.api_key_env) are caught too.
func TestConfigReadsOnlyDocumentedVariables(t *testing.T) {
	documented := documentedEnvVars(t)
	asked := map[string]bool{}
	envs := []map[string]string{
		{}, // defaults
		// AI on, so the provider-specific reads (the API key, the base URL) happen as well.
		{"AI_PROVIDER": "openai_compatible", "AI_BASE_URL": "http://ai.test/v1", "AI_MODEL": "m", "AI_API_KEY": "k"},
		{"AI_PROVIDER": "anthropic", "AI_MODEL": "m", "AI_API_KEY": "k"},
	}
	t.Chdir(t.TempDir()) // no config.yaml in the working directory
	for _, env := range envs {
		getenv := func(k string) string { asked[k] = true; return env[k] }
		_, _ = config.Load(getenv) // only the names asked for matter here
	}
	healthcheckEnv := map[string]string{"HTTP_ADDR": "nonsense"}
	healthcheck(t.Context(), func(k string) string { asked[k] = true; return healthcheckEnv[k] }, io.Discard)
	for name := range asked {
		if !documented[name] {
			t.Errorf("%s is read but not documented in env.example (every variable must be, DESIGN §19.1)", name)
		}
	}
}

// TestEnvExampleDocumentsEveryVariable is the second net: any package reading a variable by
// literal name must document it too.
func TestEnvExampleDocumentsEveryVariable(t *testing.T) {
	documented := documentedEnvVars(t)
	for name, where := range readEnvVars(t) {
		if !documented[name] {
			t.Errorf("%s is read in %s but not documented in env.example (every variable must be, DESIGN §19.1)", name, where)
		}
	}
	for _, name := range []string{
		"POSTGRES_PASSWORD", "DATABASE_URL", "PUBLIC_URL", "OPERATOR_PHONES", "HTTP_ADDR", "LOG_LEVEL",
		"CONFIG_FILE", "AI_PROVIDER", "AI_BASE_URL", "AI_MODEL", "AI_API_KEY", "AI_TIMEOUT",
	} {
		if !documented[name] {
			t.Errorf("env.example lacks %s (DESIGN §19.1 lists it)", name)
		}
	}
}

func TestComposeFileBasics(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	compose := string(src)
	for _, want := range []struct{ text, why string }{
		{"image: ghcr.io/rayanalyasi/antispam-bot:0.1", "the bot image is pinned to a minor version, never latest (DESIGN §23.4)"},
		{"image: postgres:18", "Postgres is pinned to a major version"},
		{`test: ["CMD", "/antispam", "healthcheck"]`, "the bot has a healthcheck that works in the distroless image"},
		{"pg_isready", "Postgres has a healthcheck"},
		{"condition: service_healthy", "the bot waits for a healthy Postgres"},
		{"${POSTGRES_PASSWORD:?", "a missing password fails fast instead of starting an open database"},
		{"127.0.0.1:8080:8080", "the API is only published on loopback"},
	} {
		if !strings.Contains(compose, want.text) {
			t.Errorf("docker-compose.yml lacks %q: %s", want.text, want.why)
		}
	}
	if strings.Contains(compose, ":latest") {
		t.Errorf("docker-compose.yml uses a :latest tag; pin versions (DESIGN §23.4)")
	}
	if regexp.MustCompile(`(?m)^\s+- "?(?:[0-9.]+:)?5432:5432`).MatchString(compose) {
		t.Errorf("docker-compose.yml publishes Postgres; it must never be exposed (DESIGN §19.3)")
	}
}
