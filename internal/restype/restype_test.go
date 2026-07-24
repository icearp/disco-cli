package restype

import (
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/managed"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/volatile"
)

func TestEmitReturnsCoverageDecl(t *testing.T) {
	got := Emit(Descriptor{
		Type:         "test:svc:full",
		Service:      "svc",
		Upstream:     "Test::Svc::Full",
		Uncatalogued: true,
		Leaf:         true,
	})
	want := coverage.TypeDecl{
		Service:      "svc",
		DiscoType:    "test:svc:full",
		Uncatalogued: true,
		Leaf:         true,
	}
	if got != want {
		t.Fatalf("TypeDecl = %+v, want %+v", got, want)
	}
}

func TestEmitForwardsFieldRules(t *testing.T) {
	Emit(Descriptor{
		Type:     "test:svc:rules",
		Service:  "svc",
		Managed:  true,
		Redact:   []redact.Rule{{Path: "Secret", Mode: redact.RedactScalar}},
		Volatile: []string{"Token"},
	})

	if !managed.Is("test:svc:rules") {
		t.Error("Managed:true not forwarded to managed registry")
	}
	if got := redact.Apply("test:svc:rules", `{"Secret":"hunter2"}`); strings.Contains(got, "hunter2") {
		t.Errorf("redact rule not forwarded: %s", got)
	}
	if got := volatile.Apply("test:svc:rules", `{"Token":"abc","Keep":"y"}`); strings.Contains(got, "Token") {
		t.Errorf("volatile rule not forwarded: %s", got)
	}
}

func TestEmitNoRulesRegistersNothing(t *testing.T) {
	Emit(Descriptor{Type: "test:svc:bare", Service: "svc"})

	if managed.Is("test:svc:bare") {
		t.Error("bare descriptor registered as managed")
	}
	if got := redact.Apply("test:svc:bare", `{"Secret":"x"}`); !strings.Contains(got, "x") {
		t.Errorf("bare descriptor redacted a field: %s", got)
	}
}
