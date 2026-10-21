package kube

import (
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/utils"
)

// parseKubeScopes reads --scope for a cluster session, refusing anything but
// kube and kube_exec before any request so a typo costs no round trip.
func parseKubeScopes(raw []string) ([]string, error) {
	return worksession.NormalizeClusterScopes(utils.CompactStrings(raw))
}
