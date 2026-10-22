package aws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	organizationstypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const (
	orgTestOrgARN   = "arn:aws:organizations::" + testAccountID + ":organization/o-abc"
	orgTestRootARN  = "arn:aws:organizations::" + testAccountID + ":root/o-abc/r-ab12"
	orgTestOUARN    = "arn:aws:organizations::" + testAccountID + ":ou/o-abc/ou-ab12-11111111"
	orgTestAcct1ARN = "arn:aws:organizations::" + testAccountID + ":account/o-abc/111111111111"
	orgTestAcct2ARN = "arn:aws:organizations::" + testAccountID + ":account/o-abc/222222222222"
	orgTestNoARNOU  = "arn:aws:organizations::" + testAccountID + ":ou/o-abc/ou-noar-22222222"
	orgTestAcct3ARN = "arn:aws:organizations::" + testAccountID + ":account/o-abc/333333333333"
	orgTestAcct4ARN = "arn:aws:organizations::" + testAccountID + ":account/o-abc/444444444444"
)

// orgTreeHandlers answers a management-account scan of a two-root org. Root
// r-ab12 holds one OU and account 111111111111; that OU holds 222222222222.
// Root r-noarn is listed without an Arn; it holds an OU and account
// 333333333333, and its OU holds 444444444444.
func orgTreeHandlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeOrganization": func(any) (any, error) {
			return &organizations.DescribeOrganizationOutput{Organization: &organizationstypes.Organization{
				Id: sdkaws.String("o-abc"), Arn: sdkaws.String(orgTestOrgARN), MasterAccountId: sdkaws.String(testAccountID),
			}}, nil
		},
		"ListRoots": func(any) (any, error) {
			return &organizations.ListRootsOutput{Roots: []organizationstypes.Root{
				{
					Id: sdkaws.String("r-ab12"), Arn: sdkaws.String(orgTestRootARN), Name: sdkaws.String("Root"),
					PolicyTypes: []organizationstypes.PolicyTypeSummary{{Type: organizationstypes.PolicyTypeServiceControlPolicy, Status: organizationstypes.PolicyTypeStatusEnabled}},
				},
				{Id: sdkaws.String("r-noarn"), Name: sdkaws.String("NoArn")},
			}}, nil
		},
		"ListOrganizationalUnitsForParent": func(in any) (any, error) {
			switch sv(in.(*organizations.ListOrganizationalUnitsForParentInput).ParentId) {
			case "r-ab12":
				return &organizations.ListOrganizationalUnitsForParentOutput{OrganizationalUnits: []organizationstypes.OrganizationalUnit{{
					Id: sdkaws.String("ou-ab12-11111111"), Arn: sdkaws.String(orgTestOUARN), Name: sdkaws.String("Workloads"),
				}}}, nil
			case "r-noarn":
				return &organizations.ListOrganizationalUnitsForParentOutput{OrganizationalUnits: []organizationstypes.OrganizationalUnit{{
					Id: sdkaws.String("ou-noar-22222222"), Arn: sdkaws.String(orgTestNoARNOU), Name: sdkaws.String("Sandbox"),
				}}}, nil
			}
			return &organizations.ListOrganizationalUnitsForParentOutput{}, nil
		},
		"ListAccounts": func(any) (any, error) {
			return &organizations.ListAccountsOutput{Accounts: []organizationstypes.Account{
				{Id: sdkaws.String("111111111111"), Arn: sdkaws.String(orgTestAcct1ARN), Name: sdkaws.String("one")},
				{Id: sdkaws.String("222222222222"), Arn: sdkaws.String(orgTestAcct2ARN), Name: sdkaws.String("two")},
				{Id: sdkaws.String("333333333333"), Arn: sdkaws.String(orgTestAcct3ARN), Name: sdkaws.String("three")},
				{Id: sdkaws.String("444444444444"), Arn: sdkaws.String(orgTestAcct4ARN), Name: sdkaws.String("four")},
			}}, nil
		},
		"ListParents": func(in any) (any, error) {
			parent := map[string]string{
				"111111111111": "r-ab12", "222222222222": "ou-ab12-11111111",
				"333333333333": "r-noarn", "444444444444": "ou-noar-22222222",
			}[sv(in.(*organizations.ListParentsInput).ChildId)]
			return &organizations.ListParentsOutput{Parents: []organizationstypes.Parent{{Id: sdkaws.String(parent)}}}, nil
		},
		"ListPolicies": func(any) (any, error) { return &organizations.ListPoliciesOutput{}, nil },
	}
}

// TestScanOrganizations_RootStoredOnceTypedRoot pins that the tree phase stores
// each root once, typed root, and that OUs and accounts still hang under it.
// Storing it a second time under another type in the same scan merges both
// into one row whose type flips every scan, splitting a version each time.
func TestScanOrganizations_RootStoredOnceTypedRoot(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(orgTreeHandlers(), &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})

	total, _, err := scanOrganizations(context.Background(), acct, "", st, testScanID)
	if err != nil {
		t.Fatalf("scanOrganizations: %v", err)
	}
	// organization + one root + two OUs + four accounts.
	if total != 8 {
		t.Errorf("total = %d, want 8", total)
	}

	roots := rowsByNativeID(t, st, TypeOrganizationsRoot)
	if len(roots) != 1 {
		t.Fatalf("root rows = %v, want only %s", roots, orgTestRootARN)
	}
	root, ok := roots[orgTestRootARN]
	if !ok {
		t.Fatalf("root rows = %v, want %s", roots, orgTestRootARN)
	}
	if sv(root.Name) != "Root" || sv(root.Region) != "global" {
		t.Errorf("root Name/Region = %q/%q, want Root/global", sv(root.Name), sv(root.Region))
	}
	var attrs organizationstypes.Root
	if err := json.Unmarshal([]byte(root.AttributesJSON), &attrs); err != nil {
		t.Fatalf("root attributes: %v", err)
	}
	if sv(attrs.Id) != "r-ab12" || len(attrs.PolicyTypes) != 1 || attrs.PolicyTypes[0].Type != organizationstypes.PolicyTypeServiceControlPolicy {
		t.Errorf("root attributes = %s, want the SDK Root with its SCP policy type", root.AttributesJSON)
	}
	if root.ManagedByProvider {
		t.Error("root row is ManagedByProvider; its PolicyTypes are customer posture")
	}

	versions, err := st.GetResourceVersions(root.ID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Errorf("root has %d versions after one scan, want 1", len(versions))
	}

	ous := rowsByNativeID(t, st, TypeOrganizationsOU)
	if _, ok := ous[orgTestRootARN]; ok {
		t.Errorf("an organizational-unit row carries the root ARN")
	}
	if len(ous) != 2 {
		t.Errorf("OU rows = %v, want %s and %s", ous, orgTestOUARN, orgTestNoARNOU)
	}

	assertContainsNative(t, st, orgTestOrgARN, orgTestRootARN)
	assertContainsNative(t, st, orgTestRootARN, orgTestOUARN)
	assertContainsNative(t, st, orgTestRootARN, orgTestAcct1ARN)
	assertContainsNative(t, st, orgTestOUARN, orgTestAcct2ARN)
}

// TestScanOrganizations_ARNlessRootSubtreeStillStored pins that a root listed
// without an Arn is not stored (it has no NativeID) and is reported once, while
// the OUs and accounts under it are still listed and stored.
func TestScanOrganizations_ARNlessRootSubtreeStillStored(t *testing.T) {
	st := newTestStore(t)
	var (
		mu    sync.Mutex
		warns = map[string]int{}
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warns[w.Provider+"/"+w.Service]++
		mu.Unlock()
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(orgTreeHandlers(), &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})

	if _, _, err := scanOrganizations(context.Background(), acct, "", st, testScanID); err != nil {
		t.Fatalf("scanOrganizations: %v", err)
	}
	if n := warns["aws/organizations:ListRoots"]; n != 1 {
		t.Errorf("organizations:ListRoots warnings = %d, want 1", n)
	}
	// The OU's pair to the rowless root writes no contains row and is
	// reported by the store, not dropped silently.
	if n := warns["store/hierarchy"]; n != 1 {
		t.Errorf("store hierarchy warnings = %d, want 1", n)
	}
	if _, ok := rowsByNativeID(t, st, TypeOrganizationsOU)[orgTestNoARNOU]; !ok {
		t.Errorf("OU under the ARN-less root not stored")
	}
	accounts := rowsByNativeID(t, st, TypeOrganizationsAccount)
	for _, arn := range []string{orgTestAcct3ARN, orgTestAcct4ARN} {
		if _, ok := accounts[arn]; !ok {
			t.Errorf("account %s under the ARN-less root not stored", arn)
		}
	}
	assertContainsNative(t, st, orgTestNoARNOU, orgTestAcct4ARN)
	for _, typ := range []string{TypeOrganizationsRoot, TypeOrganizationsOU, TypeOrganizationsAccount} {
		if _, ok := rowsByNativeID(t, st, typ)[""]; ok {
			t.Errorf("a %s row has an empty NativeID", typ)
		}
	}
}

// A denied ListParents keeps every account, unlinked, and warns once; an
// account that left the organization (ChildNotFoundException) is stored
// unlinked and warned about once; any other error fails the scan.
func TestScanOrganizations_ListParentsErrors(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "organizations:ListParents")
		h := orgTreeHandlers()
		h["ListParents"] = func(any) (any, error) {
			return nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized to perform: organizations:ListParents"}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})
		if _, _, err := scanOrganizations(context.Background(), acct, "", st, testScanID); err != nil {
			t.Fatalf("scanOrganizations: %v", err)
		}
		if *warns != 1 {
			t.Errorf("organizations:ListParents warnings = %d, want 1", *warns)
		}
		accounts := rowsByNativeID(t, st, TypeOrganizationsAccount)
		if len(accounts) != 4 {
			t.Fatalf("account rows = %v, want the 4 listed", nativeIDs(accounts))
		}
		rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, orgTestRootARN))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rels {
			if r.ToID == store.ResourceID("aws", testAccountID, orgTestAcct1ARN) {
				t.Errorf("root contains account %s although its parent was not read", orgTestAcct1ARN)
			}
		}
	})
	t.Run("account gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "organizations:ListParents")
		h := orgTreeHandlers()
		parents := h["ListParents"]
		h["ListParents"] = func(in any) (any, error) {
			if sv(in.(*organizations.ListParentsInput).ChildId) == "111111111111" {
				return nil, &smithy.GenericAPIError{Code: "ChildNotFoundException", Message: "gone"}
			}
			return parents(in)
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})
		total, _, err := scanOrganizations(context.Background(), acct, "", st, testScanID)
		if err != nil || *warns != 1 || total != 8 {
			t.Fatalf("total = %d, warnings = %d, err = %v; want 8 rows, 1 warning for the unlinked account, nil", total, *warns, err)
		}
		assertContainsNative(t, st, orgTestOUARN, orgTestAcct2ARN)
		rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, orgTestRootARN))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rels {
			if r.ToID == store.ResourceID("aws", testAccountID, orgTestAcct1ARN) {
				t.Errorf("root contains account %s although ListParents no longer found it", orgTestAcct1ARN)
			}
		}
	})
	// One account's ListParents fails mid-page: every account is still stored,
	// the failed one unlinked. A throttle warns once and is no error; any
	// other error is returned after the accounts are stored.
	oneFails := func(err error) map[string]func(in any) (any, error) {
		h := orgTreeHandlers()
		parents := h["ListParents"]
		h["ListParents"] = func(in any) (any, error) {
			if sv(in.(*organizations.ListParentsInput).ChildId) == "111111111111" {
				return nil, err
			}
			return parents(in)
		}
		return h
	}
	for _, tc := range []struct {
		name      string
		err       error
		wantWarns int
		wantCode  string
	}{
		{"throttled", &smithy.GenericAPIError{Code: "TooManyRequestsException", Message: "slow down"}, 1, ""},
		{"other error", &smithy.GenericAPIError{Code: "InvalidInputException", Message: "bad"}, 0, "InvalidInputException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:ListParents")
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(oneFails(tc.err), &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})
			_, _, err := scanOrganizations(context.Background(), acct, "", st, testScanID)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scanOrganizations: %v", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("organizations:ListParents warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if n := len(rowsByNativeID(t, st, TypeOrganizationsAccount)); n != 4 {
				t.Errorf("account rows = %d, want the 4 listed", n)
			}
			// scanOrganizations records the closure only when every phase
			// before it succeeded, so the links are checked on success only.
			if tc.wantCode == "" {
				assertContainsNative(t, st, orgTestOUARN, orgTestAcct2ARN)
			}
		})
	}
	// The "stored without their parent" warning counts only accounts stored:
	// a failed write returns the store error and no warning.
	t.Run("denied but not stored", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		warns := countOpWarnings(st, "organizations:ListParents")
		h := orgTreeHandlers()
		h["ListParents"] = func(any) (any, error) {
			closeStore()
			return nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized to perform: organizations:ListParents"}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})
		_, _, err := scanOrganizations(context.Background(), acct, "", st, testScanID)
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Errorf("err = %v, want the store write failure", err)
		}
		if *warns != 0 {
			t.Errorf("organizations:ListParents warnings = %d, want 0: no account was stored", *warns)
		}
	})
}

// DescribeResourcePolicy's expected states (no policy, no organization) store
// nothing silently; a denial warns once; any other error fails the scan.
func TestScanOrganizations_DescribeResourcePolicyErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantWarns int
		wantCode  string
	}{
		{"denied", &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized to perform: organizations:DescribeResourcePolicy"}, 1, ""},
		{"no policy", &smithy.GenericAPIError{Code: "ResourcePolicyNotFoundException", Message: "none"}, 0, ""},
		{"other error", &smithy.GenericAPIError{Code: "ServiceException", Message: "boom"}, 0, "ServiceException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:DescribeResourcePolicy")
			h := orgTreeHandlers()
			h["DescribeResourcePolicy"] = func(any) (any, error) { return nil, tc.err }
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})
			_, _, err := scanOrganizations(context.Background(), acct, "", st, testScanID)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scanOrganizations: %v", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("organizations:DescribeResourcePolicy warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if rows := rowsByNativeID(t, st, TypeOrganizationsResourcePolicy); len(rows) != 0 {
				t.Errorf("resource policy rows = %v, want none", nativeIDs(rows))
			}
		})
	}
}
