package restype

import (
	"runtime"
	"sync"
)

// Origin ties every registered type to the scanner service registered from
// the same source file. Scan records name failures by scanner service
// ("aws:sso-admin"), types name their API service ("sso"); the two differ
// for dozens of services and nothing but the file links them, so the link is
// taken from the call site rather than a hand-maintained map.
type Origin struct {
	mu       sync.Mutex
	types    map[string]string   // disco type -> file
	services map[string][]string // file -> scanner service names
}

// NoteType records the file of registerType's caller. Call it from the
// provider's registerType only: the depth assumes exactly one frame between.
func (o *Origin) NoteType(discoType string) {
	_, file, _, ok := runtime.Caller(2)
	if !ok {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.types == nil {
		o.types = map[string]string{}
	}
	o.types[discoType] = file
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
func (o *Origin) TypeServices() map[string][]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make(map[string][]string, len(o.types))
	for t, file := range o.types {
		if svcs := o.services[file]; len(svcs) > 0 {
			out[t] = append([]string(nil), svcs...)
		}
	}
	return out
}
