package restype

import (
	"runtime"
	"strings"
	"sync"
)

// Origin ties every registered type to the scanner service registered from
// the same source file. Scan records name failures by scanner service
// ("aws:sso-admin"), types name their API service ("sso"); the two differ
// for dozens of services and nothing but the file links them, so the link is
// taken from the call site rather than a hand-maintained map.
type Origin struct {
	mu       sync.Mutex
	types    map[string]typeOrigin // disco type -> where it was registered
	services map[string][]string   // file -> scanner service names
}

// typeOrigin is one type's registration site: the file, and the API service
// the descriptor declares. The declared service is what tells two types apart
// when their file registers several scanner services.
type typeOrigin struct {
	file            string
	declaredService string
}

// NoteType records the file of registerType's caller together with the
// service the descriptor declares. Call it from the provider's registerType
// only: the depth assumes exactly one frame between.
func (o *Origin) NoteType(discoType, declaredService string) {
	_, file, _, ok := runtime.Caller(2)
	if !ok {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.types == nil {
		o.types = map[string]typeOrigin{}
	}
	o.types[discoType] = typeOrigin{file: file, declaredService: declaredService}
}

// NoteService records the file of registerService's caller; same depth rule.
func (o *Origin) NoteService(name string) {
	_, file, _, ok := runtime.Caller(2)
	if !ok {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.services == nil {
		o.services = map[string][]string{}
	}
	o.services[file] = append(o.services[file], name)
}

// TypeServices maps every noted type to the scanner services registered from
// its file; a type whose file registers no service is absent.
//
// A file registering several services (apigateway + apigatewayv2, the three
// GCP database services) used to give every one of its types every one of
// those names, so one service's failure explained types belonging to its
// neighbours. There the declared service decides, and a type it matches none
// of is left out rather than attributed to all of them.
func (o *Origin) TypeServices() map[string][]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make(map[string][]string, len(o.types))
	for t, org := range o.types {
		svcs := o.services[org.file]
		if len(svcs) > 1 {
			svcs = narrowByDeclared(svcs, org.declaredService)
		}
		if len(svcs) > 0 {
			out[t] = append([]string(nil), svcs...)
		}
	}
	return out
}

// narrowByDeclared keeps the registered names that answer to the declared
// service: an exact match when there is one, else the names that extend it or
// that it extends ("bigtableadmin" declares "gcp:bigtable"). Scanner names and
// declared services are written differently on purpose, which is why this
// compares normalised forms rather than requiring equality.
func narrowByDeclared(svcs []string, declared string) []string {
	want := normalizeService(declared)
	if want == "" {
		return nil
	}
	var exact, loose []string
	for _, s := range svcs {
		got := normalizeService(s)
		switch {
		case got == want:
			exact = append(exact, s)
		case strings.HasPrefix(got, want) || strings.HasPrefix(want, got):
			loose = append(loose, s)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return loose
}

// normalizeService reduces a scanner name ("aws:sso-admin") or a declared
// service ("sso") to the form the two can be compared in. The leading "cloud"
// goes too, and from both sides equally, so the API service "run" reaches the
// scanner "gcp:cloudrun" without "cloudtrail" and "cloudfront" colliding.
func normalizeService(s string) string {
	if _, rest, found := strings.Cut(s, ":"); found {
		s = rest
	}
	s = strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(s))
	if rest := strings.TrimPrefix(s, "cloud"); rest != "" {
		return rest
	}
	return s
}
