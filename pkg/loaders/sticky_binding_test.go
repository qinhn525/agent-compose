package loaders

import (
	"testing"

	domain "agent-compose/pkg/model"
)

func TestRetiringLoaderBindingPreservesSandboxAndTracksDesiredConfig(t *testing.T) {
	binding := domain.LoaderBinding{
		LoaderID:          "loader-1",
		TriggerID:         "trigger-1",
		SandboxID:         "sandbox-1",
		SandboxConfigHash: "sha256:old",
	}
	retiring := RetiringLoaderBinding(binding, " sha256:new ")
	if retiring.LoaderID != binding.LoaderID || retiring.TriggerID != binding.TriggerID || retiring.SandboxID != binding.SandboxID {
		t.Fatalf("retiring binding changed identity: got %#v want %#v", retiring, binding)
	}
	if desiredHash, ok := RetiringLoaderBindingConfigHash(retiring); !ok || desiredHash != "sha256:new" {
		t.Fatalf("RetiringLoaderBindingConfigHash = %q/%v, want sha256:new/true", desiredHash, ok)
	}
	if _, ok := RetiringLoaderBindingConfigHash(binding); ok {
		t.Fatal("ordinary binding reported as retiring")
	}
}

func TestAdoptLegacyLoaderBindingConfigHash(t *testing.T) {
	binding := domain.LoaderBinding{
		LoaderID:  "loader-1",
		TriggerID: "trigger-1",
		SandboxID: "sandbox-1",
	}
	adopted, ok := AdoptLegacyLoaderBindingConfigHash(binding, " sha256:current ")
	if !ok {
		t.Fatal("legacy binding was not adopted")
	}
	if adopted.LoaderID != binding.LoaderID || adopted.TriggerID != binding.TriggerID || adopted.SandboxID != binding.SandboxID {
		t.Fatalf("adopted binding changed identity: got %#v want %#v", adopted, binding)
	}
	if adopted.SandboxConfigHash != "sha256:current" {
		t.Fatalf("adopted config hash = %q, want sha256:current", adopted.SandboxConfigHash)
	}
	if binding.SandboxConfigHash != "" {
		t.Fatalf("AdoptLegacyLoaderBindingConfigHash mutated its input: %#v", binding)
	}

	for name, test := range map[string]struct {
		binding domain.LoaderBinding
		desired string
	}{
		"current binding": {binding: adopted, desired: "sha256:other"},
		"empty desired":   {binding: binding, desired: ""},
		"retiring binding": {
			binding: RetiringLoaderBinding(binding, "sha256:current"),
			desired: "sha256:current",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := AdoptLegacyLoaderBindingConfigHash(test.binding, test.desired)
			if ok || got != test.binding {
				t.Fatalf("AdoptLegacyLoaderBindingConfigHash = %#v/%v, want unchanged/false", got, ok)
			}
		})
	}
}
