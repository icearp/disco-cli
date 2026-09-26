package restype

import (
	"slices"
	"testing"
)

// noteTypeAt and noteServiceAt stand in for a provider's registerType /
// registerService, which is the frame depth NoteType/NoteService assume.
func noteTypeAt(o *Origin, discoType, declared string) { o.NoteType(discoType, declared) }
func noteServiceAt(o *Origin, name string)             { o.NoteService(name) }

// A file registering one service claims every type registered beside it; one
// registering several claims only the types whose declared service answers to
// it, rather than all of them (apigateway + apigatewayv2 in one file, the
// three GCP database services in another).
func TestTypeServices_MultiServiceFileNarrowsByDeclaredService(t *testing.T) {
	var o Origin
	noteTypeAt(&o, "aws:apigateway:rest-api", "apigateway")
	noteTypeAt(&o, "aws:apigatewayv2:api", "apigatewayv2")
	noteTypeAt(&o, "gcp:bigtable:table", "bigtableadmin")
	noteTypeAt(&o, "aws:apigateway:mystery", "somethingelse")
	noteServiceAt(&o, "aws:apigateway")
	noteServiceAt(&o, "aws:apigatewayv2")

	got := o.TypeServices()
	for _, tc := range []struct {
		typ  string
		want []string
	}{
		{"aws:apigateway:rest-api", []string{"aws:apigateway"}},
		{"aws:apigatewayv2:api", []string{"aws:apigatewayv2"}},
		// "bigtableadmin" matches neither registered name, so it is left
		// unclaimed instead of blamed on both.
		{"gcp:bigtable:table", nil},
		{"aws:apigateway:mystery", nil},
	} {
		if !slices.Equal(got[tc.typ], tc.want) {
			t.Errorf("TypeServices()[%q] = %v; want %v", tc.typ, got[tc.typ], tc.want)
		}
	}
}

// The single-service file keeps the plain join: that is the case the whole
// mechanism exists for, and the declared service often disagrees with the
// scanner name there ("sso" vs "aws:sso-admin").
func TestTypeServices_SingleServiceFileClaimsEveryType(t *testing.T) {
	var o Origin
	noteTypeAt(&o, "aws:sso:instance", "sso")
	noteTypeAt(&o, "aws:sso:permission-set", "sso")
	noteServiceAt(&o, "aws:sso-admin")

	got := o.TypeServices()
	for _, typ := range []string{"aws:sso:instance", "aws:sso:permission-set"} {
		if !slices.Equal(got[typ], []string{"aws:sso-admin"}) {
			t.Errorf("TypeServices()[%q] = %v; want [aws:sso-admin]", typ, got[typ])
		}
	}
}

func TestNarrowByDeclared_PrefersExactOverPrefix(t *testing.T) {
	svcs := []string{"gcp:bigtable", "gcp:firestore", "gcp:spanner"}
	if got := narrowByDeclared(svcs, "bigtable"); !slices.Equal(got, []string{"gcp:bigtable"}) {
		t.Errorf("exact = %v; want [gcp:bigtable]", got)
	}
	// A longer declared name still reaches its scanner, and only its scanner.
	if got := narrowByDeclared(svcs, "spannerv2"); !slices.Equal(got, []string{"gcp:spanner"}) {
		t.Errorf("prefix = %v; want [gcp:spanner]", got)
	}
	if got := narrowByDeclared(svcs, ""); got != nil {
		t.Errorf("empty declared = %v; want nil", got)
	}
}
