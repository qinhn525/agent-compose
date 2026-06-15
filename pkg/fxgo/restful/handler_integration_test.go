package restful

import "testing"

func TestIntegrationSPAFileServerWorkflow(t *testing.T) {
	testSPAFileServerHandlerServesAssetsAndFallsBackToIndex(t)
	testSPAFileServerHandlerRedirectsPrefixWithoutSlash(t)
}

func TestE2ESPAFileServerWorkflow(t *testing.T) {
	testSPAFileServerHandlerServesAssetsAndFallsBackToIndex(t)
	testSPAFileServerHandlerRedirectsPrefixWithoutSlash(t)
}
