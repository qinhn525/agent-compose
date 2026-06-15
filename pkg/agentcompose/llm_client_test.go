package agentcompose

import (
	appconfig "agent-compose/pkg/config"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLLMClientResolveEndpointPrefersGlobalEnvThenProcessEnvThenDefault(t *testing.T) {
	ctx := context.Background()
	store := newTestConfigStore(t)
	client := &LLMClient{config: &appconfig.Config{LLMAPIEndpoint: "https://config.example.invalid"}, configDB: store}

	t.Setenv("LLM_API_ENDPOINT", "https://env.example.invalid")
	if got := client.resolveEndpoint(ctx); got != "https://env.example.invalid/v1/responses" {
		t.Fatalf("resolveEndpoint from process env = %q, want %q", got, "https://env.example.invalid/v1/responses")
	}

	if _, err := store.ReplaceGlobalEnv(ctx, []SessionEnvVar{{Name: "LLM_API_ENDPOINT", Value: "https://db.example.invalid", Secret: false}}); err != nil {
		t.Fatalf("ReplaceGlobalEnv returned error: %v", err)
	}
	if got := client.resolveEndpoint(ctx); got != "https://db.example.invalid/v1/responses" {
		t.Fatalf("resolveEndpoint from db env = %q, want %q", got, "https://db.example.invalid/v1/responses")
	}

	if _, err := store.ReplaceGlobalEnv(ctx, nil); err != nil {
		t.Fatalf("ReplaceGlobalEnv(reset) returned error: %v", err)
	}
	if err := os.Unsetenv("LLM_API_ENDPOINT"); err != nil {
		t.Fatalf("Unsetenv returned error: %v", err)
	}
	if got := client.resolveEndpoint(ctx); got != "https://config.example.invalid/v1/responses" {
		t.Fatalf("resolveEndpoint from config fallback = %q, want %q", got, "https://config.example.invalid/v1/responses")
	}

	client.config.LLMAPIEndpoint = ""
	if got := client.resolveEndpoint(ctx); got != "https://api.openai.com/v1/responses" {
		t.Fatalf("resolveEndpoint default = %q, want %q", got, "https://api.openai.com/v1/responses")
	}
}

func TestNormalizeLLMAPIEndpointKeepsExplicitPath(t *testing.T) {
	if got := normalizeLLMAPIEndpoint("https://api.example.invalid/v1/responses"); got != "https://api.example.invalid/v1/responses" {
		t.Fatalf("normalizeLLMAPIEndpoint explicit path = %q, want unchanged", got)
	}
	if got := normalizeLLMAPIEndpoint("https://api.example.invalid/custom/path"); got != "https://api.example.invalid/custom/path" {
		t.Fatalf("normalizeLLMAPIEndpoint custom path = %q, want unchanged", got)
	}
}

func TestLLMClientGenerateHandlesSuccessAndFailures(t *testing.T) {
	testLLMClientGenerateHandlesSuccessAndFailures(t)
}

func testLLMClientGenerateHandlesSuccessAndFailures(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	t.Setenv("LLM_API_ENDPOINT", "")
	store := newTestConfigStore(t)
	if _, err := store.ReplaceGlobalEnv(ctx, []SessionEnvVar{
		{Name: "LLM_API_KEY", Value: "env-key", Secret: true},
	}); err != nil {
		t.Fatalf("ReplaceGlobalEnv returned error: %v", err)
	}

	var gotAuth string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody = readRequestBodyForTest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"model-a","status":"completed","output":[{"finish_reason":"stop","content":[{"text":"hello"},{"text":"world"}]}]}`))
	}))
	t.Cleanup(server.Close)

	client := &LLMClient{
		config:   &appconfig.Config{LLMAPIEndpoint: server.URL, LLMAPIKey: "fallback-key", LLMModel: "fallback-model"},
		configDB: store,
		client:   server.Client(),
	}
	result, err := client.Generate(ctx, "prompt", "model-a", "")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.Text != "hello\nworld" || result.Model != "model-a" || result.ResponseID != "resp-1" || result.FinishReason != "stop" {
		t.Fatalf("unexpected generate result: %+v", result)
	}
	if gotAuth != "Bearer env-key" {
		t.Fatalf("authorization header = %q, want %q", gotAuth, "Bearer env-key")
	}
	if !strings.Contains(gotBody, `"input":"prompt"`) || !strings.Contains(gotBody, `"model":"model-a"`) {
		t.Fatalf("request body missing prompt/model: %s", gotBody)
	}

	if _, err := client.Generate(ctx, "   ", "model-a", ""); err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("Generate(empty prompt) error = %v, want prompt error", err)
	}

	failureServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	t.Cleanup(failureServer.Close)
	client.config.LLMAPIEndpoint = failureServer.URL
	client.client = failureServer.Client()
	if _, err := client.Generate(ctx, "prompt", "model-a", ""); err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("Generate(failure response) error = %v, want bad request", err)
	}
}

func TestLLMClientGenerateSendsOutputSchema(t *testing.T) {
	ctx := context.Background()
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = readRequestBodyForTest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"model-a","status":"completed","output_text":"{\"answer\":\"ok\"}"}`))
	}))
	t.Cleanup(server.Close)

	client := &LLMClient{
		config: &appconfig.Config{
			LLMAPIEndpoint: server.URL,
			LLMModel:       "model-a",
		},
		configDB: newTestConfigStore(t),
		client:   server.Client(),
	}
	schema := `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`
	result, err := client.Generate(ctx, "prompt", "", schema)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.Text != `{"answer":"ok"}` {
		t.Fatalf("text = %q, want structured JSON text", result.Text)
	}
	for _, want := range []string{`"text"`, `"format"`, `"type":"json_schema"`, `"name":"agent_compose_llm_output"`, `"strict":true`, `"schema":{"type":"object"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("request body %s missing %s", gotBody, want)
		}
	}
	if _, err := client.Generate(ctx, "prompt", "", "{bad json"); err == nil || !strings.Contains(err.Error(), "outputSchema must be valid JSON") {
		t.Fatalf("Generate(invalid schema) error = %v, want schema error", err)
	}
}

func readRequestBodyForTest(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("ReadAll request body returned error: %v", err)
	}
	return string(body)
}
