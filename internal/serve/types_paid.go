//go:build paid

// Package serve implements the disco scan-trigger HTTP API. Two routes:
// POST /v1/scans (async scan submission) and GET /v1/healthz (liveness).
// JWT-gated, tenant-pinned, one-shot per container — see openapi.yaml.
package serve

import "encoding/json"

// ScanRequest mirrors the JSON body schema in openapi.yaml. Empty slices
// fall through to the provider's default scope (e.g. all regions on AWS).
type ScanRequest struct {
	Provider      string   `json:"provider"`
	Regions       []string `json:"regions,omitempty"`
	Accounts      []string `json:"accounts,omitempty"`
	Subscriptions []string `json:"subscriptions,omitempty"`
	Projects      []string `json:"projects,omitempty"`
	ResourceTypes []string `json:"resource_types,omitempty"`
}

// ScanAccepted is the 202 reply body.
type ScanAccepted struct {
	ScanID string `json:"scan_id"`
	Status string `json:"status"` // always "pending"
}

// errorEnvelope wraps API failures as `{error: {code, message, details}}`.
// Hand-formed (no third-party error lib) to keep the dep graph tight.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// errorCodes are the ones the server emits. Kept as constants so
// handler / middleware / tests reference one source.
const (
	errCodeUnauthorized      = "unauthorized"
	errCodeTenantMismatch    = "tenant_mismatch"
	errCodeBadRequest        = "bad_request"
	errCodeCredsInBody       = "credentials_in_body_forbidden"
	errCodeScanInProgress    = "scan_in_progress"
	errCodeInternal          = "internal_error"
	errCodeUnknownProvider   = "unknown_provider"
	errCodeMethodNotAllowed  = "method_not_allowed"
	errCodeUnsupportedMedia  = "unsupported_media_type"
)

// rawJSONOK marshals v as the 2xx response body.
func rawJSONOK(v any) ([]byte, error) {
	return json.Marshal(v)
}
