//go:build paid

// Package policy is the paid-only Rego policy engine for `disco check`.
// Resources from the local store are handed to a prepared Rego query; the
// query is expected to bind `data.disco.deny` to a set of finding objects.
//
// This is a scaffold — no first-party policies ship yet. Real policy bundles
// (Conftest AWS, regula, internal CIS packs) are loaded via NewEngine paths.
package policy

import (
	"context"
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/open-policy-agent/opa/v1/rego"
)

// denyQuery is the canonical entrypoint every disco-compatible Rego module
// must populate. Conftest convention so third-party packs drop in unchanged.
const denyQuery = "data.disco.deny"

// Finding is the slim, JSON-friendly shape produced by the engine. Field
// names match the Rego object keys callers must emit.
type Finding struct {
	ID          string            `json:"id"`
	Severity    string            `json:"severity"`
	Message     string            `json:"message"`
	ResourceID  string            `json:"resource_id"`
	Provider    string            `json:"provider,omitempty"`
	Type        string            `json:"type,omitempty"`
	Name        string            `json:"name,omitempty"`
	Region      string            `json:"region,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Remediation string            `json:"remediation,omitempty"`
	RefURL      string            `json:"ref_url,omitempty"`
}

// Engine wraps a prepared Rego query. Build once per scan, evaluate per
// resource — PrepareForEval amortises parsing/compilation across the loop.
type Engine struct {
	pq rego.PreparedEvalQuery
}

// NewEngine compiles the Rego modules under paths (files or directories,
// recursive) into a prepared query. Empty paths yields an engine that
// evaluates against an empty policy set — useful for smoke tests.
func NewEngine(ctx context.Context, paths []string) (*Engine, error) {
	opts := []func(*rego.Rego){rego.Query(denyQuery)}
	if len(paths) > 0 {
		opts = append(opts, rego.Load(paths, nil))
	}
	pq, err := rego.New(opts...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile rego: %w", err)
	}
	return &Engine{pq: pq}, nil
}

// NewEngineFromModules builds an engine from in-memory Rego source. Used by
// tests; production callers should prefer NewEngine + on-disk policies.
func NewEngineFromModules(ctx context.Context, modules map[string]string) (*Engine, error) {
	opts := []func(*rego.Rego){rego.Query(denyQuery)}
	for name, src := range modules {
		opts = append(opts, rego.Module(name, src))
	}
	pq, err := rego.New(opts...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile rego: %w", err)
	}
	return &Engine{pq: pq}, nil
}

// Evaluate runs the prepared query against each resource and aggregates
// every emitted Finding. Resource attributes are decoded from
// AttributesJSON so policies can address fields directly without parsing.
func (e *Engine) Evaluate(ctx context.Context, resources []store.Resource) ([]Finding, error) {
	var out []Finding
	for _, r := range resources {
		input, err := resourceToInput(&r)
		if err != nil {
			return nil, err
		}
		rs, err := e.pq.Eval(ctx, rego.EvalInput(input))
		if err != nil {
			return nil, fmt.Errorf("eval %s: %w", r.ID, err)
		}
		for _, result := range rs {
			for _, expr := range result.Expressions {
				findings, err := decodeFindings(expr.Value, &r)
				if err != nil {
					return nil, err
				}
				out = append(out, findings...)
			}
		}
	}
	return out, nil
}

// resourceToInput builds the Rego input document. AttributesJSON decodes
// into a nested object so policies can write `input.attributes.Encrypted`
// rather than re-parsing the string.
func resourceToInput(r *store.Resource) (map[string]any, error) {
	var attrs any
	if r.AttributesJSON != "" {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			attrs = r.AttributesJSON
		}
	}
	return map[string]any{
		"id":         r.ID,
		"provider":   r.Provider,
		"account_id": r.AccountID,
		"type":       r.Type,
		"native_id":  r.NativeID,
		"name":       derefOrEmpty(r.Name),
		"region":     derefOrEmpty(r.Region),
		"status":     derefOrEmpty(r.Status),
		"attributes": attrs,
	}, nil
}

// decodeFindings turns the raw Rego expression value into typed Findings.
// `data.disco.deny` is conventionally a set, surfaced as []any.
func decodeFindings(v any, r *store.Resource) ([]Finding, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("deny: want []any, got %T", v)
	}
	out := make([]Finding, 0, len(items))
	for _, item := range items {
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		var f Finding
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("decode finding: %w", err)
		}
		if f.ResourceID == "" {
			f.ResourceID = r.ID
		}
		if f.Provider == "" {
			f.Provider = r.Provider
		}
		if f.Type == "" {
			f.Type = r.Type
		}
		if f.Name == "" {
			f.Name = derefOrEmpty(r.Name)
		}
		if f.Region == "" {
			f.Region = derefOrEmpty(r.Region)
		}
		out = append(out, f)
	}
	return out, nil
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
