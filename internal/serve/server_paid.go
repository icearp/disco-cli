//go:build paid

package serve

import (
	"encoding/json"
	"errors"
	"net/http"

	"codeberg.org/icearp/disco/internal/scanrun"
)

// Config is the wiring NewServer needs. All fields required.
type Config struct {
	Runner    *Runner
	JWTSecret []byte
	TenantID  string // pinned by container env; JWT `tenant` claim must match
}

// NewServer builds the http.Handler. Stdlib net/http with Go 1.22+
// pattern-matched ServeMux — no chi, no router lib. /v1/healthz is the
// only unauthenticated route; everything else flows through jwtMiddleware.
func NewServer(cfg Config) http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated. Used by ECS task health checks / Lambda readiness.
	mux.HandleFunc("GET /v1/healthz", handleHealthz)

	// Authenticated. Wrapped via the per-route adapter so the middleware
	// only runs on /v1/scans, not on healthz.
	mux.Handle("POST /v1/scans", jwtMiddleware(cfg.JWTSecret, cfg.TenantID, handleScansSubmit(cfg.Runner)))

	// Method/path-not-found go through writeError to keep the JSON
	// envelope consistent. http.NotFoundHandler / MethodNotAllowed default
	// to plain text otherwise.
	return wrapNotFound(mux)
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleScansSubmit decodes the body, scrubs it for forbidden keys,
// builds a scanrun.Request, and submits to the runner. Returns 202 on
// success, 409 if a scan is already in-flight, 400 on body issues.
func handleScansSubmit(runner *Runner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if ct != "" && ct != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, errCodeUnsupportedMedia,
				"expected application/json")
			return
		}
		// Decode twice: once into a generic map for the cred-scrub recursion,
		// once into the typed struct. Two-pass keeps the scrub logic agnostic
		// of struct shape so adding a forbidden key requires only an entry in
		// forbiddenBodyKeys, no struct change.
		var raw map[string]any
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&raw); err != nil {
			writeError(w, http.StatusBadRequest, errCodeBadRequest, "invalid json: "+err.Error())
			return
		}
		if hit := scrubBody(raw); hit != "" {
			writeErrorWithDetails(w, http.StatusBadRequest, errCodeCredsInBody,
				"request body carries a forbidden key; server config is pinned at startup",
				map[string]any{"forbidden_key": hit})
			return
		}

		// Re-encode the same map back through json into the typed struct.
		// Avoids re-reading the request body (already consumed) and is fast
		// for tiny objects.
		jsBytes, _ := json.Marshal(raw)
		var req ScanRequest
		if err := json.Unmarshal(jsBytes, &req); err != nil {
			writeError(w, http.StatusBadRequest, errCodeBadRequest, "schema: "+err.Error())
			return
		}
		if req.Provider == "" {
			writeError(w, http.StatusBadRequest, errCodeBadRequest, "provider is required")
			return
		}

		scanID, err := runner.Submit(r.Context(), scanrun.Request{
			Providers:     []string{req.Provider},
			Regions:       req.Regions,
			Accounts:      req.Accounts,
			Subscriptions: req.Subscriptions,
			Projects:      req.Projects,
			ResourceTypes: req.ResourceTypes,
		})
		if err != nil {
			if errors.Is(err, ErrScanInProgress) {
				writeError(w, http.StatusConflict, errCodeScanInProgress,
					"a scan is already running in this container; submit to a fresh task")
				return
			}
			writeErrorWithDetails(w, http.StatusBadRequest, errCodeUnknownProvider, err.Error(), nil)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		body, _ := rawJSONOK(ScanAccepted{ScanID: scanID, Status: "pending"})
		_, _ = w.Write(body)
	})
}

// wrapNotFound intercepts the default 404 / 405 plaintext fallthrough
// from net/http.ServeMux and serves the canonical JSON error envelope.
func wrapNotFound(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux exposes Handler(r) which returns the matched handler +
		// pattern. Empty pattern means no match.
		_, pattern := mux.Handler(r)
		if pattern == "" {
			writeError(w, http.StatusNotFound, errCodeMethodNotAllowed,
				"no route for "+r.Method+" "+r.URL.Path)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
