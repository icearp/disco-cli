package gcp

import (
	"encoding/json"
	"slices"
	"testing"
)

func newSchemaSet(t *testing.T, doc string) *schemaSet {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &raw); err != nil {
		t.Fatal(err)
	}
	return &schemaSet{raw: raw}
}

// TestElement_PrefersCollectionNoun: ListJobsResponse carries failedLocation
// beside jobs, and taking the alphabetically first array gave dataflow/jobs
// FailedLocation's zero refs over Job's references.
func TestElement_PrefersCollectionNoun(t *testing.T) {
	ss := newSchemaSet(t, `{
	  "ListJobsResponse": {"properties": {
	    "failedLocation": {"type": "array", "items": {"$ref": "FailedLocation"}},
	    "jobs": {"type": "array", "items": {"$ref": "Job"}},
	    "nextPageToken": {"type": "string"}
	  }},
	  "FailedLocation": {"properties": {"name": {"type": "string"}}},
	  "Job": {"properties": {
	    "id": {"type": "string"},
	    "revisionId": {"type": "string", "description": "The URL of the revision."},
	    "$schema": {"type": "string", "description": "A reference to another schema."},
	    "replaceJobId": {"type": "string", "description": "The ID of the job to replace."},
	    "environment": {"$ref": "Environment"}
	  }},
	  "Environment": {"properties": {
	    "serviceKmsKeyName": {"type": "string", "description": "The KMS key resource name."}
	  }}
	}`)
	if got := ss.element("ListJobsResponse", "jobs"); got != "Job" {
		t.Fatalf("element = %q, want Job", got)
	}
	want := []string{"environment.serviceKmsKeyName", "replaceJobId"}
	if got := ss.refsOf("ListJobsResponse", "jobs"); !slices.Equal(got, want) {
		t.Errorf("refsOf = %v, want %v (revisionId and $schema are not refs)", got, want)
	}
}

// TestElement_RichestWhenNounMatchesNothing: with no name matching the
// collection, the array whose item schema carries the most properties wins.
func TestElement_RichestWhenNounMatchesNothing(t *testing.T) {
	ss := newSchemaSet(t, `{
	  "Resp": {"properties": {
	    "alpha": {"type": "array", "items": {"$ref": "Thin"}},
	    "beta": {"type": "array", "items": {"$ref": "Fat"}}
	  }},
	  "Thin": {"properties": {"name": {"type": "string"}}},
	  "Fat": {"properties": {"name": {"type": "string"}, "networkUrl": {"type": "string"}, "zone": {"type": "string"}}}
	}`)
	if got := ss.element("Resp", "widgets"); got != "Fat" {
		t.Errorf("element = %q, want Fat", got)
	}
}
