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
