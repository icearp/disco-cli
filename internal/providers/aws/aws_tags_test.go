package aws

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"testing"

	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
	acotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizerautomation/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	firehosetypes "github.com/aws/aws-sdk-go-v2/service/firehose/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	wellarchitectedtypes "github.com/aws/aws-sdk-go-v2/service/wellarchitected/types"
)

// TestAWSTagKV_CoversUnion fails when an awsTag union member has no case in
// awsTagKV's type switch (or a case names a non-member). The compiler accepts a
// missing case, and awsTagKV would then panic only when a scan meets that type.
func TestAWSTagKV_CoversUnion(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "aws_tags.go", nil, 0)
	if err != nil {
		t.Fatalf("parse aws_tags.go: %v", err)
	}
	union := map[string]bool{}
	cases := map[string]bool{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			collectAWSTagUnion(d, union)
		case *ast.FuncDecl:
			if d.Name.Name == "awsTagKV" {
				collectCaseTypes(d.Body, cases)
			}
		}
	}
	if len(union) == 0 || len(cases) == 0 {
		t.Fatalf("found %d awsTag members and %d awsTagKV cases in aws_tags.go; this test no longer finds them", len(union), len(cases))
	}
	for m := range union {
		if !cases[m] {
			t.Errorf("awsTag member %s has no case in awsTagKV", m)
		}
	}
	for c := range cases {
		if !union[c] {
			t.Errorf("awsTagKV case %s is not an awsTag member", c)
		}
	}
}

func collectAWSTagUnion(d *ast.GenDecl, into map[string]bool) {
	for _, spec := range d.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "awsTag" {
			continue
		}
		iface, ok := ts.Type.(*ast.InterfaceType)
		if !ok {
			continue
		}
		for _, elem := range iface.Methods.List {
			collectUnionTerms(elem.Type, into)
		}
	}
}

func collectUnionTerms(e ast.Expr, into map[string]bool) {
	if bin, ok := e.(*ast.BinaryExpr); ok && bin.Op == token.OR {
		collectUnionTerms(bin.X, into)
		collectUnionTerms(bin.Y, into)
		return
	}
	into[types.ExprString(e)] = true
}

func collectCaseTypes(body *ast.BlockStmt, into map[string]bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		if cc, ok := n.(*ast.CaseClause); ok {
			for _, e := range cc.List {
				into[types.ExprString(e)] = true
			}
		}
		return true
	})
}

// TestAWSTagsJSON_AllUnionMembers checks that each awsTagKV case reads its own
// type's Key and Value; TestAWSTagKV_CoversUnion only checks that the case exists.
func TestAWSTagsJSON_AllUnionMembers(t *testing.T) {
	k, v := "K", "V"
	tests := []struct {
		name string
		got  *string
	}{
		{"acm", awsTagsJSON([]acmtypes.Tag{{Key: &k, Value: &v}})},
		{"computeoptimizerautomation", awsTagsJSON([]acotypes.Tag{{Key: &k, Value: &v}})},
		{"cloudformation", awsTagsJSON([]cfntypes.Tag{{Key: &k, Value: &v}})},
		{"cloudfront", awsTagsJSON([]cloudfronttypes.Tag{{Key: &k, Value: &v}})},
		{"cloudhsmv2", awsTagsJSON([]cloudhsmv2types.Tag{{Key: &k, Value: &v}})},
		{"ec2", awsTagsJSON([]ec2types.Tag{{Key: &k, Value: &v}})},
		{"ecr", awsTagsJSON([]ecrtypes.Tag{{Key: &k, Value: &v}})},
		{"ecs", awsTagsJSON([]ecstypes.Tag{{Key: &k, Value: &v}})},
		{"elasticache", awsTagsJSON([]elasticachetypes.Tag{{Key: &k, Value: &v}})},
		{"firehose", awsTagsJSON([]firehosetypes.Tag{{Key: &k, Value: &v}})},
		{"iam", awsTagsJSON([]iamtypes.Tag{{Key: &k, Value: &v}})},
		{"kinesis", awsTagsJSON([]kinesistypes.Tag{{Key: &k, Value: &v}})},
		{"rds", awsTagsJSON([]rdstypes.Tag{{Key: &k, Value: &v}})},
		{"route53", awsTagsJSON([]route53types.Tag{{Key: &k, Value: &v}})},
		{"wellarchitected", awsTagsJSON([]wellarchitectedtypes.Tag{{Key: &k, Value: &v}})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTagsMap(t, tt.got, map[string]string{k: v})
		})
	}
}

// assertTagsMap decodes a tag-JSON pointer and compares it with want.
func assertTagsMap(t *testing.T, got *string, want map[string]string) {
	t.Helper()
	if got == nil {
		t.Fatalf("got nil, want %v", want)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*got), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", *got, err)
	}
	if !maps.Equal(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

// TestAWSTagsJSON_NilKeySkippedNilValueKept: a nil Key has nothing to key the
// entry by, so the tag is skipped; a nil Value (the ElastiCache model documents
// "May be null") keeps the key with "".
func TestAWSTagsJSON_NilKeySkippedNilValueKept(t *testing.T) {
	k, keyOnly, v := "Real", "KeyOnly", "Value"
	tags := []elasticachetypes.Tag{
		{Key: nil, Value: &v},
		{Key: &keyOnly, Value: nil},
		{Key: &k, Value: &v},
	}
	assertTagsMap(t, awsTagsJSON(tags), map[string]string{"Real": "Value", "KeyOnly": ""})
}

// kvTestTag stands in for an SDK tag type outside the awsTag union.
type kvTestTag struct{ K, V *string }

func kvTestTagKV(t kvTestTag) (key, value *string) { return t.K, t.V }

func TestKVTagsJSON_EmptyIsNil(t *testing.T) {
	if got := kvTagsJSON(nil, kvTestTagKV); got != nil {
		t.Errorf("kvTagsJSON(nil) = %q, want nil", *got)
	}
	if got := kvTagsJSON([]kvTestTag{}, kvTestTagKV); got != nil {
		t.Errorf("kvTagsJSON(empty) = %q, want nil", *got)
	}
	v := "orphan"
	if got := kvTagsJSON([]kvTestTag{{K: nil, V: &v}}, kvTestTagKV); got != nil {
		t.Errorf("kvTagsJSON(only nil keys) = %q, want nil", *got)
	}
}

func TestKVTagsJSON_ReadsThroughAccessor(t *testing.T) {
	k1, k2, keyOnly, v := "env", "team", "flag", "prod"
	tags := []kvTestTag{
		{K: &k1, V: &v},
		{K: &k2, V: &v},
		{K: nil, V: &v},
		{K: &keyOnly, V: nil},
	}
	assertTagsMap(t, kvTagsJSON(tags, kvTestTagKV), map[string]string{"env": "prod", "team": "prod", "flag": ""})
}

// TestMapTagsJSON covers the alternative helper for services whose SDK returns
// tags as map[string]string rather than typed slices.
func TestMapTagsJSON(t *testing.T) {
	if got := mapTagsJSON(nil); got != nil {
		t.Errorf("nil map: got %q, want nil", *got)
	}
	if got := mapTagsJSON(map[string]string{}); got != nil {
		t.Errorf("empty map: got %q, want nil", *got)
	}
	got := mapTagsJSON(map[string]string{"env": "prod", "team": "core"})
	if got == nil {
		t.Fatal("nil result")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*got), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["env"] != "prod" || m["team"] != "core" {
		t.Errorf("round-trip: %v", m)
	}
}

// TestAWSTagsJSON_EC2Tags verifies that EC2 SDK tag slices are converted to
// the expected JSON map format stored in resources.tags.
func TestAWSTagsJSON_EC2Tags(t *testing.T) {
	k1, v1, k2, v2 := "Name", "web-server", "env", "prod"
	tags := []ec2types.Tag{
		{Key: &k1, Value: &v1},
		{Key: &k2, Value: &v2},
	}

	result := awsTagsJSON(tags)
	if result == nil {
		t.Fatal("awsTagsJSON: got nil, want non-nil")
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(*result), &m); err != nil {
		t.Fatalf("awsTagsJSON: result is not valid JSON: %v", err)
	}
	if m["Name"] != "web-server" {
		t.Errorf("Name tag: got %q, want %q", m["Name"], "web-server")
	}
	if m["env"] != "prod" {
		t.Errorf("env tag: got %q, want %q", m["env"], "prod")
	}
}

// TestAWSTagsJSON_Empty verifies that nil is returned for an empty tag slice,
// matching the nullable column expectation in the database.
func TestAWSTagsJSON_Empty(t *testing.T) {
	if got := awsTagsJSON([]ec2types.Tag{}); got != nil {
		t.Errorf("awsTagsJSON(empty): got %q, want nil", *got)
	}
}

// TestEC2TagName_Found verifies that the "Name" tag value is extracted.
func TestEC2TagName_Found(t *testing.T) {
	k1, v1 := "env", "prod"
	k2, v2 := "Name", "my-instance"
	tags := []ec2types.Tag{
		{Key: &k1, Value: &v1},
		{Key: &k2, Value: &v2},
	}
	got := ec2TagName(tags)
	if got == nil || *got != "my-instance" {
		t.Errorf("ec2TagName: got %v, want %q", got, "my-instance")
	}
}

// TestEC2TagName_Absent verifies that nil is returned when no "Name" tag exists.
func TestEC2TagName_Absent(t *testing.T) {
	k1, v1 := "env", "prod"
	tags := []ec2types.Tag{{Key: &k1, Value: &v1}}
	if got := ec2TagName(tags); got != nil {
		t.Errorf("ec2TagName (no Name tag): got %q, want nil", *got)
	}
}

// TestEC2TagName_EmptySlice verifies that nil is returned for an empty tag slice.
func TestEC2TagName_EmptySlice(t *testing.T) {
	if got := ec2TagName(nil); got != nil {
		t.Errorf("ec2TagName(nil): got %q, want nil", *got)
	}
}
