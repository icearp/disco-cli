package sdkinv

import (
	"reflect"
	"testing"
)

func TestCanonAndSingular(t *testing.T) {
	for in, want := range map[string]string{
		"virtualMachines": "virtualmachine", "virtual-machines": "virtualmachine", "Databases": "database",
		"snoozes": "snooze", "policies": "policy", "indexes": "index", "addresses": "address", "status": "status",
		"aliases": "alias", "statuses": "status", "lenses": "lens", "analyses": "analysis", "cases": "case",
		"releases": "release", "alias": "alias", "accountAlias": "accountalias", "DBInstances": "dbinstance", "AWS::EC2::Instance": "awsec2instance",
		// Shapes the suffix rules cannot spell, each one a key that shipped
		// wrong in docs/coverage.md: attachedindice, thesauri, timesery,
		// applicationshadercach, receivedlicens, len.
		"indices": "index", "attachedIndices": "attachedindex", "matrices": "matrix", "vertices": "vertex",
		"thesauri": "thesaurus", "TimeSeries": "timeseries", "revenueStatisticsTimeSeries": "revenuestatisticstimeseries",
		"caches": "cache", "applicationShaderCaches": "applicationshadercache", "batches": "batch", "branches": "branch",
		"niches": "niche", "beaches": "beach", "approaches": "approach", "speeches": "speech",
		"receivedLicenses": "receivedlicense", "licenses": "license", "ephemeris": "ephemeris", "species": "species",
	} {
		if got := CanonSingular(in); got != want {
			t.Errorf("CanonSingular(%q) = %q, want %q", in, got, want)
		}
	}
	for plural, singular := range map[string]string{
		"anywhereCaches": "anywhere-cache", "aliases": "alias", "statuses": "status", "databases": "database",
		"policies": "policy", "indexes": "index", "instances": "instance", "addresses": "address", "accesses": "access", "keys": "key",
		"analyses": "analysis", "lenses": "lens", "cases": "case",
		// The Latin plurals: "indices" and "index" met nowhere, so qbusiness
		// counted one index collection twice.
		"indices": "index", "attachedIndices": "attachedIndex", "matrices": "matrix",
		"vertices": "vertex", "thesauri": "thesaurus",
	} {
		if Ident(plural) != Ident(singular) {
			t.Errorf("Ident(%q) = %q, Ident(%q) = %q", plural, Ident(plural), singular, Ident(singular))
		}
	}
}

func TestKebab(t *testing.T) {
	for in, want := range map[string]string{
		"virtualMachineScaleSets": "virtual-machine-scale-sets", "p2sVpnGateways": "p2s-vpn-gateways",
		"dedicatedHSMs": "dedicated-hsms", "DBInstance": "db-instance", "instances": "instances", "IPAllocations": "ip-allocations",
	} {
		if got := Kebab(in); got != want {
			t.Errorf("Kebab(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripScopes(t *testing.T) {
	scopes := map[string]bool{"subscriptions": true, "resourcegroups": true, "projects": true, "locations": true}
	cases := []struct {
		tmpl string
		want ResourcePath
	}{
		{"/subscriptions/{s}/providers/Microsoft.Compute/virtualMachines", ResourcePath{Statics: []string{"providers", "Microsoft.Compute", "virtualMachines"}}},
		{"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Compute/virtualMachines/{vm}/extensions", ResourcePath{Statics: []string{"providers", "Microsoft.Compute", "virtualMachines", "extensions"}, Parents: []string{"virtualMachines"}}},
		{"/subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.Compute/virtualMachines/{vm}", ResourcePath{Statics: []string{"providers", "Microsoft.Compute", "virtualMachines"}, Item: true}},
		{"projects/{p}/locations/{l}/jobs/{j}/executions", ResourcePath{Statics: []string{"jobs", "executions"}, Parents: []string{"jobs"}}},
		{"b/{bucket}/anywhereCaches", ResourcePath{Statics: []string{"b", "anywhereCaches"}, Parents: []string{"b"}}},
	}
	for _, c := range cases {
		got := StripScopes(ParseTemplate(c.tmpl), scopes, nil)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("StripScopes(%q) = %+v, want %+v", c.tmpl, got, c.want)
		}
	}
}
