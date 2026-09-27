package azure

import (
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
)

func TestARMEndpointReason(t *testing.T) {
	cases := map[string]string{
		"operations":                                               coverage.ReasonARMOperation,
		"locations/operationresults":                               coverage.ReasonARMOperation,
		"locations/operationstatuses":                              coverage.ReasonARMOperation,
		"servers/administratorazureasyncoperation":                 coverage.ReasonARMOperation,
		"manageddatabasemoveoperationresults":                      coverage.ReasonARMOperation,
		"operationresults/profileresults/policyresults":            coverage.ReasonARMOperation,
		"namespaces/disasterrecoveryconfigs/checknameavailability": coverage.ReasonARMOperation,
		"locations":                       coverage.ReasonLocationScoped,
		"locations/diagnosticruncommands": coverage.ReasonLocationScoped,
		// Real resources that share a word with the rules.
		"servers":                                "",
		"instances/brokers/listeners":            "",
		"validatedsolutionrecipes":               "",
		"clusters/databases/eventhubconnections": "",
		"workspaces/purge":                       "",
		"virtualmachinescanceloperations":        "",
		"locationbasedcapabilities":              "",
	}
	for typ, want := range cases {
		if got := armEndpointReason(typ); got != want {
			t.Errorf("armEndpointReason(%q) = %q, want %q", typ, got, want)
		}
	}
}
