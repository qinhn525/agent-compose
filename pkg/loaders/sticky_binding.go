package loaders

import (
	"strings"

	domain "agent-compose/pkg/model"
)

const retiringLoaderBindingConfigPrefix = "retiring:"

// RetiringLoaderBinding returns a compare-and-swap replacement that makes an
// existing sticky sandbox unavailable for reuse before its runtime is stopped.
// The sandbox ID is retained so another request can finish the retirement if
// the request that claimed it exits early.
func RetiringLoaderBinding(binding domain.LoaderBinding, desiredConfigHash string) domain.LoaderBinding {
	binding.SandboxConfigHash = retiringLoaderBindingConfigPrefix + strings.TrimSpace(desiredConfigHash)
	return binding
}

// RetiringLoaderBindingConfigHash reports the configuration that a sticky
// binding retirement is preparing to install.
func RetiringLoaderBindingConfigHash(binding domain.LoaderBinding) (string, bool) {
	hash, found := strings.CutPrefix(strings.TrimSpace(binding.SandboxConfigHash), retiringLoaderBindingConfigPrefix)
	if !found {
		return "", false
	}
	return strings.TrimSpace(hash), true
}
