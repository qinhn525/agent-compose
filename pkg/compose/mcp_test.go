package compose

import (
	"strings"
	"testing"
)

func TestParseMCPConfig(t *testing.T) {
	spec, err := Parse([]byte(`
name: mcp-demo
mcps:
  filesystem:
    type: local
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
    env:
      TOKEN:
        value: ${MCP_TOKEN}
        secret: true
  docs:
    type: remote
    transport: http
    url: https://docs.example.com/mcp
    headers:
      Authorization:
        value: Bearer ${DOCS_TOKEN}
        secret: true
agents:
  reviewer:
    provider: codex
    mcps: [filesystem, docs]
  writer:
    provider: claude
    mcps:
      - filesystem
      - name: notes
        type: local
        command: uvx
        args: [notes-server]
`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(spec.MCPs) != 2 {
		t.Fatalf("mcps = %#v", spec.MCPs)
	}
	if got := spec.MCPs["filesystem"].Command; got != "npx" {
		t.Fatalf("filesystem command = %q", got)
	}
	if got := spec.Agents["reviewer"].MCPs; len(got) != 2 || got[0].Ref != "filesystem" || got[1].Ref != "docs" {
		t.Fatalf("reviewer mcps = %#v", got)
	}
	if got := spec.Agents["writer"].MCPs; len(got) != 2 || got[0].Ref != "filesystem" || got[1].Name != "notes" || got[1].Command != "uvx" {
		t.Fatalf("writer mcps = %#v", got)
	}
}

func TestParseRejectsInvalidMCPRefType(t *testing.T) {
	spec, err := Parse([]byte(`
name: invalid-mcp-ref
agents:
  reviewer:
    provider: codex
    mcps:
      name: docs
`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	_, err = Normalize(spec, NormalizeOptions{})
	if err == nil {
		t.Fatalf("expected Normalize to fail")
	}
	if got := err.Error(); !strings.Contains(got, "agents.reviewer.mcps[0].type") {
		t.Fatalf("error = %q, want mcp path", got)
	}
}

func TestNormalizeMCPConfig(t *testing.T) {
	spec := mustParseCompose(t, `
name: mcp-demo
mcps:
  filesystem:
    type: local
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
    env:
      TOKEN:
        value: ${MCP_TOKEN}
        secret: true
  docs:
    type: remote
    transport: http
    url: https://docs.example.com/${DOCS_PATH}
    headers:
      Authorization:
        value: Bearer ${DOCS_TOKEN}
        secret: true
agents:
  reviewer:
    provider: codex
    mcps:
      - filesystem
      - docs
      - filesystem
      - name: notes
        type: local
        command: uvx
        args: [notes-server]
`)

	normalized, err := Normalize(spec, NormalizeOptions{Env: map[string]string{
		"MCP_TOKEN":  "mcp-secret",
		"DOCS_TOKEN": "docs-secret",
		"DOCS_PATH":  "mcp",
	}})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if got := normalized.MCPs["filesystem"].Env["TOKEN"]; got.Value != "mcp-secret" || !got.Secret {
		t.Fatalf("filesystem env = %#v", got)
	}
	if got := normalized.MCPs["docs"].URL; got != "https://docs.example.com/mcp" {
		t.Fatalf("docs url = %q", got)
	}
	if got := normalized.Agents[0].MCPs; len(got) != 3 || got["filesystem"].Command != "npx" || got["notes"].Command != "uvx" {
		t.Fatalf("agent mcps = %#v", got)
	}
}

func TestNormalizeRejectsUndefinedMCPRef(t *testing.T) {
	spec := mustParseCompose(t, `
name: undefined-mcp
mcps:
  filesystem:
    type: local
    command: npx
agents:
  reviewer:
    provider: codex
    mcps: [filesystem, missing]
`)

	_, err := Normalize(spec, NormalizeOptions{})
	if err == nil {
		t.Fatalf("expected Normalize to fail")
	}
	if got := err.Error(); !strings.Contains(got, `mcp "missing" is not defined`) {
		t.Fatalf("error = %q", got)
	}
}

func TestNormalizeRejectsInvalidMCPShape(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantField string
	}{
		{
			name: "local requires command",
			raw: `
name: bad-local
mcps:
  filesystem:
    type: local
agents:
  reviewer:
    provider: codex
`,
			wantField: "mcps.filesystem.command",
		},
		{
			name: "remote requires transport",
			raw: `
name: bad-remote
mcps:
  docs:
    type: remote
    url: https://docs.example.com/mcp
agents:
  reviewer:
    provider: codex
`,
			wantField: "mcps.docs.transport",
		},
		{
			name: "remote forbids env",
			raw: `
name: bad-remote-env
mcps:
  docs:
    type: remote
    transport: http
    url: https://docs.example.com/mcp
    env:
      TOKEN: x
agents:
  reviewer:
    provider: codex
`,
			wantField: "mcps.docs.env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := mustParseCompose(t, tt.raw)
			_, err := Normalize(spec, NormalizeOptions{})
			if err == nil {
				t.Fatalf("expected Normalize to fail")
			}
			if got := err.Error(); !strings.Contains(got, tt.wantField) {
				t.Fatalf("error = %q, want field %s", got, tt.wantField)
			}
		})
	}
}

func TestRedactedOutputDoesNotLeakMCPSecrets(t *testing.T) {
	spec := mustParseCompose(t, `
name: redacted-mcp
mcps:
  docs:
    type: remote
    transport: http
    url: https://docs.example.com/mcp
    headers:
      Authorization:
        value: Bearer ${DOCS_TOKEN}
        secret: true
agents:
  reviewer:
    provider: codex
    mcps: docs
`)
	normalized, err := Normalize(spec, NormalizeOptions{Env: map[string]string{"DOCS_TOKEN": "super-secret"}})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	data, err := normalized.MarshalCanonicalYAML(true)
	if err != nil {
		t.Fatalf("MarshalCanonicalYAML returned error: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "super-secret") {
		t.Fatalf("redacted yaml leaked secret: %s", text)
	}
	if !strings.Contains(text, redactedEnvValue) || !strings.Contains(text, "mcps:") {
		t.Fatalf("redacted yaml = %s", text)
	}
}
