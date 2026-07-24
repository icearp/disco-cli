package aws

import (
	"github.com/icearp/disco-cli/store"
)

func init() { registerResolver(resolveAccessAnalyzerRelationships) }

// resolveAccessAnalyzerRelationships is a no-op by design.
//
// AnalyzerSummary fields surfaced by ListAnalyzers (Arn, Name, Status, Type,
// Tags, LastResourceAnalyzed*, StatusReason) carry no cross-resource ARNs.
// Configuration — when populated by GetAnalyzer — is a Smithy union
// (UnusedAccessConfiguration / InternalAccessConfiguration); its leaf fields
// (ExclusionsAccountIds, ResourceTypes) are bare account IDs / type strings,
// not ARNs to scanned resources. Type=Organization analyzers run against the
// caller's org but the AnalyzerSummary itself does not embed an org id.
//
// Audit: scanned the SDK type 2026-04-30 (accessanalyzer v1.47.2). If a future
// SDK release adds an ARN-bearing field to AnalyzerSummary or its
// Configuration, wire edges here.
func resolveAccessAnalyzerRelationships(_ *account, _ *store.Store) error {
	return nil
}
