// Package policy is the Rego policy engine for `disco check`. Resources
// from the local store are handed to a prepared Rego query; the query is
// expected to bind `data.disco.deny` to a set of finding objects.
//
// Bring your own policies (Conftest AWS, regula, in-house bundles) via the
// `--rules` flag. Curated first-party compliance packs (NIST 800-53, CIS,
// PCI-DSS, Well-Architected) are future work, not yet bundled.
package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"codeberg.org/icearp/disco/store"
	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/storage"
	"github.com/open-policy-agent/opa/v1/storage/inmem"
	"github.com/open-policy-agent/opa/v1/topdown"
)

// denyQuery is the canonical entrypoint every disco-compatible Rego module
// must populate. Conftest convention so third-party packs drop in unchanged.
const denyQuery = "data.disco.deny"

// queryBinding is the variable the prepared body assigns the deny-set to.
// Topdown queries bind expressions to vars; we read this var out of each
// QueryResult to recover the Rego value.
const queryBinding = "deny"

// InputContractVersion identifies the shape of the input.* document handed
// to Rego policies. Bumped on any breaking change to field names or types
// so BYO rules can pin against a known contract via
// `input.contract_version == "1"` rather than failing silently when keys
// rename. The version is stamped into every resource input.
const InputContractVersion = "1"

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

// Engine wraps a compiled Rego module set and the parsed deny-query body.
// Build once per scan, evaluate per resource — compilation amortises across
// the loop. Uses the lower-level `ast` + `topdown` packages directly rather
// than `rego` to avoid pulling `internal/compiler/wasm` (~780 KB precompiled
// blob) into the binary; disco only ever evaluates against topdown.
type Engine struct {
	compiler *ast.Compiler
	body     ast.Body
	store    storage.Store
}

// NewEngine compiles the Rego modules under paths (files or directories,
// recursive) AND any in-memory modules into a single compiler. Either
// argument may be empty — passing both empty yields an engine evaluating
// against an empty policy set (useful for smoke tests). Used by `disco
// check` to compose `--rules <dir>` with `--packs aws-waf` in one pass.
func NewEngine(_ context.Context, paths []string, modules map[string]string) (*Engine, error) {
	parsed := map[string]*ast.Module{}
	for _, p := range paths {
		if err := loadRegoFiles(p, parsed); err != nil {
			return nil, err
		}
	}
	for name, src := range modules {
		m, err := ast.ParseModuleWithOpts(name, src, ast.ParserOptions{})
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		parsed[name] = m
	}
	c := ast.NewCompiler()
	c.Compile(parsed)
	if c.Failed() {
		return nil, fmt.Errorf("compile rego: %w", c.Errors)
	}
	body, err := ast.ParseBody(queryBinding + " = " + denyQuery)
	if err != nil {
		return nil, fmt.Errorf("parse query: %w", err)
	}
	return &Engine{compiler: c, body: body, store: inmem.New()}, nil
}

// loadRegoFiles walks a path (file or directory) and parses every .rego
// file it finds into out, keyed by absolute file path so compile errors
// point back to source. Mirrors the recursion `rego.Load` did.
func loadRegoFiles(path string, out map[string]*ast.Module) error {
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".rego") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		m, perr := ast.ParseModuleWithOpts(p, string(b), ast.ParserOptions{})
		if perr != nil {
			return fmt.Errorf("parse %s: %w", p, perr)
		}
		out[p] = m
		return nil
	})
}

// Evaluate runs the prepared query against each resource and aggregates
// every emitted Finding. Resource attributes are decoded from
// AttributesJSON so policies can address fields directly without parsing.
func (e *Engine) Evaluate(ctx context.Context, resources []store.Resource) ([]Finding, error) {
	txn, err := e.store.NewTransaction(ctx)
	if err != nil {
		return nil, fmt.Errorf("open txn: %w", err)
	}
	defer e.store.Abort(ctx, txn)

	var out []Finding
	denyVar := ast.Var(queryBinding)
	for _, r := range resources {
		input, err := resourceToInput(&r)
		if err != nil {
			return nil, err
		}
		inputVal, err := ast.InterfaceToValue(input)
		if err != nil {
			return nil, fmt.Errorf("input %s: %w", r.ID, err)
		}
		q := topdown.NewQuery(e.body).
			WithCompiler(e.compiler).
			WithStore(e.store).
			WithTransaction(txn).
			WithInput(ast.NewTerm(inputVal))
		ierr := q.Iter(ctx, func(qr topdown.QueryResult) error {
			term, ok := qr[denyVar]
			if !ok {
				return nil
			}
			raw, jerr := ast.JSON(term.Value)
			if jerr != nil {
				return fmt.Errorf("json: %w", jerr)
			}
			findings, derr := decodeFindings(raw, &r)
			if derr != nil {
				return derr
			}
			out = append(out, findings...)
			return nil
		})
		if ierr != nil {
			return nil, fmt.Errorf("eval %s: %w", r.ID, ierr)
		}
	}
	return out, nil
}

// resourceToInput builds the Rego input document. AttributesJSON / TagsJSON
// decode into nested objects so policies can write `input.attributes.Encrypted`
// or `input.tags.env`. Custody timestamps + scan-run IDs surface so policies
// can express freshness-bound controls (`time.parse_rfc3339_ns(input.verified_at)`).
func resourceToInput(r *store.Resource) (map[string]any, error) {
	var attrs any
	if r.AttributesJSON != "" {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			attrs = r.AttributesJSON
		}
	}
	var tags any = map[string]any{}
	if r.TagsJSON != nil && *r.TagsJSON != "" {
		var parsed any
		if err := json.Unmarshal([]byte(*r.TagsJSON), &parsed); err == nil {
			tags = parsed
		} else {
			tags = *r.TagsJSON
		}
	}
	return map[string]any{
		"contract_version":    InputContractVersion,
		"id":                  r.ID,
		"provider":            r.Provider,
		"account_id":          r.AccountID,
		"account_name":        derefOrEmpty(r.AccountName),
		"type":                r.Type,
		"native_id":           r.NativeID,
		"name":                derefOrEmpty(r.Name),
		"region":              derefOrEmpty(r.Region),
		"zone":                derefOrEmpty(r.Zone),
		"status":              derefOrEmpty(r.Status),
		"tags":                tags,
		"attributes":          attrs,
		"created_at":          derefOrEmpty(r.CreatedAt),
		"discovered_at":       r.DiscoveredAt,
		"discovered_by":       r.DiscoveredBy,
		"managed_by_provider": r.ManagedByProvider,
	}, nil
}

// decodeFindings turns the raw Rego value into typed Findings.
// `data.disco.deny` is conventionally a set, surfaced as []any after
// ast.JSON conversion.
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
