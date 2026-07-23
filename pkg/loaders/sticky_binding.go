package loaders

import (
	"strings"

	domain "agent-compose/pkg/model"
)

const retiringLoaderBindingConfigPrefix = "retiring:"

// AdoptLegacyLoaderBindingConfigHash returns a replacement that records the
// desired configuration on a binding created before configuration hashes were
// persisted. The caller must install the replacement with compare-and-swap so
// concurrent requests cannot adopt different configurations.
func AdoptLegacyLoaderBindingConfigHash(binding domain.LoaderBinding, desiredConfigHash string) (domain.LoaderBinding, bool) {
	desiredConfigHash = strings.TrimSpace(desiredConfigHash)
	if strings.TrimSpace(binding.SandboxConfigHash) != "" || desiredConfigHash == "" {
		return binding, false
	}
	binding.SandboxConfigHash = desiredConfigHash
	return binding, true
}

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
