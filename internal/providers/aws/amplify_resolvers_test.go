package aws

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	amplifytypes "github.com/aws/aws-sdk-go-v2/service/amplify/types"
	"github.com/icearp/disco-cli/store"
)

func ampRoleARN(name string) string { return "arn:aws:iam::" + testAccountID + ":role/" + name }

// assertUsesEdges checks from's outbound uses edges are exactly to wants.
func assertUsesEdges(t *testing.T, st *store.Store, from string, wants ...string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(from, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", from, err)
	}
	got := make([]string, 0, len(rels))
	for _, r := range rels {
		got = append(got, r.ToID)
	}
	slices.Sort(got)
	wants = slices.Clone(wants)
	slices.Sort(wants)
	if !slices.Equal(got, wants) {
		t.Errorf("uses edges from %s = %v, want %v", from, got, wants)
	}
}

// Apps link to their service and compute roles and branches to their compute
// role, only when the role was scanned; a row without role ARNs gets no edge.
func TestResolveAmplifyRelationships_RoleEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	svcRole := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, ampRoleARN("svc"), "", "{}")
	computeRole := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, ampRoleARN("ssr"), "", "{}")
	appARN := ampAppARN("a")
	app := upsertTestResource(t, st, "aws", testAccountID, TypeAmplifyApp, appARN, testRegion, mustJSON(amplifytypes.App{
		AppArn: &appARN, IamServiceRoleArn: ptrStr(ampRoleARN("svc")), ComputeRoleArn: ptrStr(ampRoleARN("ssr")),
	}))
	unscannedARN := ampAppARN("b")
	unscanned := upsertTestResource(t, st, "aws", testAccountID, TypeAmplifyApp, unscannedARN, testRegion, mustJSON(amplifytypes.App{
		AppArn: &unscannedARN, IamServiceRoleArn: ptrStr(ampRoleARN("never-scanned")),
	}))
	bareARN := ampAppARN("c")
	bare := upsertTestResource(t, st, "aws", testAccountID, TypeAmplifyApp, bareARN, testRegion, mustJSON(amplifytypes.App{AppArn: &bareARN}))
	branchARN := ampBranchARN("a", "main")
	branch := upsertTestResource(t, st, "aws", testAccountID, TypeAmplifyBranch, branchARN, testRegion, mustJSON(amplifytypes.Branch{
		BranchArn: &branchARN, ComputeRoleArn: ptrStr(ampRoleARN("ssr")),
	}))
	bareBranchARN := ampBranchARN("a", "dev")
	bareBranch := upsertTestResource(t, st, "aws", testAccountID, TypeAmplifyBranch, bareBranchARN, testRegion, mustJSON(amplifytypes.Branch{BranchArn: &bareBranchARN}))

	if err := resolveAmplifyRelationships(acct, st); err != nil {
		t.Fatalf("resolveAmplifyRelationships: %v", err)
	}
	assertUsesEdges(t, st, app, svcRole, computeRole)
	assertUsesEdges(t, st, branch, computeRole)
	assertUsesEdges(t, st, unscanned)
	assertUsesEdges(t, st, bare)
	assertUsesEdges(t, st, bareBranch)
}

// A row whose attributes do not decode is returned as an error after the
// other rows' edges are written.
func TestResolveAmplifyRelationships_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	role := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, ampRoleARN("ssr"), "", "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeAmplifyApp, ampAppARN("bad"), testRegion, `{"IamServiceRoleArn":5}`)
	upsertTestResource(t, st, "aws", testAccountID, TypeAmplifyBranch, ampBranchARN("bad", "main"), testRegion, `{"ComputeRoleArn":5}`)
	branchARN := ampBranchARN("a", "main")
	branch := upsertTestResource(t, st, "aws", testAccountID, TypeAmplifyBranch, branchARN, testRegion, mustJSON(amplifytypes.Branch{
		BranchArn: &branchARN, ComputeRoleArn: ptrStr(ampRoleARN("ssr")),
	}))

	err := resolveAmplifyRelationships(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the attributes' decode error", err)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); !ok || len(joined.Unwrap()) != 2 {
		t.Errorf("err = %v, want two joined errors, one per bad row", err)
	}
	assertUsesEdges(t, st, branch, role)
}

// A failed edge write is returned joined with the decode error of a row read
// before it, from each of the three edge sites: app service role, app compute
// role, branch compute role. Rows list by name, so app "a-bad" comes first.
func TestResolveAmplifyRelationships_WriteFailureKeepsDecodeErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		app    *amplifytypes.App
		branch *amplifytypes.Branch
	}{
		{"app service role", &amplifytypes.App{IamServiceRoleArn: ptrStr(ampRoleARN("r"))}, nil},
		{"app compute role", &amplifytypes.App{ComputeRoleArn: ptrStr(ampRoleARN("r"))}, nil},
		{"branch compute role", nil, &amplifytypes.Branch{ComputeRoleArn: ptrStr(ampRoleARN("r"))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, ampRoleARN("r"), "", "{}")
			upsertTestResourceNamed(t, st, TypeAmplifyApp, ampAppARN("bad"), testRegion, `{"IamServiceRoleArn":5}`, "a-bad")
			if tc.app != nil {
				upsertTestResourceNamed(t, st, TypeAmplifyApp, ampAppARN("good"), testRegion, mustJSON(tc.app), "b-good")
			}
			if tc.branch != nil {
				upsertTestResourceNamed(t, st, TypeAmplifyBranch, ampBranchARN("good", "main"), testRegion, mustJSON(tc.branch), "main")
			}
			if _, err := st.DB().Exec(`CREATE TRIGGER refuse_edge BEFORE INSERT ON relationships
				BEGIN SELECT RAISE(ABORT, 'test: edge write refused'); END`); err != nil {
				t.Fatalf("create trigger: %v", err)
			}
			err := resolveAmplifyRelationships(acct, st)
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) || !errors.Is(err, store.ErrStoreWrite) {
				t.Errorf("err = %v; want the store write failure joined with the bad app's *json.UnmarshalTypeError", err)
			}
		})
	}
}
