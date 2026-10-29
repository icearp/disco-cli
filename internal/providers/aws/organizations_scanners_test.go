package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

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

	// The rows orgFullHandlers adds to the tree.
	orgTestDeepOUARN     = "arn:aws:organizations::" + testAccountID + ":ou/o-abc/ou-ab12-33333333"
	orgTestCustomSCPARN  = "arn:aws:organizations::" + testAccountID + ":policy/o-abc/service_control_policy/p-custom111"
	orgTestFullAccessARN = "arn:aws:organizations::aws:policy/service_control_policy/p-FullAWSAccess"
	orgTestRPARN         = "arn:aws:organizations::" + testAccountID + ":resourcepolicy/o-abc/rp-abcd1234"
	orgTestOutARN        = "arn:aws:organizations::" + testAccountID + ":transfer/o-abc/billing/outbound/rt-out11111"
	// A transfer whose ARN names another organization's management account.
	// Which list returns such ARNs is unverified live; the scanner keys on
	// the ARN's account, whatever the list.
	orgTestInARN = "arn:aws:organizations::999999999999:transfer/o-other1/billing/inbound/rt-in111111"
	// An AWS-managed SCP other than FullAWSAccess.
	orgTestAWSManagedSCPARN = "arn:aws:organizations::aws:policy/service_control_policy/p-awsmanaged1"
	// org + root + 3 OUs + 4 accounts + 3 SCPs + resource policy + 2 transfers.
	orgTestFullTotal = 15
)

var orgTestDenied = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}

// orgTestTagged holds the ResourceIds whose resources the fixtures tag, each
// with the one tag id=<its Id>; every other resource has no tags.
var orgTestTagged = map[string]bool{
	"r-ab12": true, "ou-ab12-11111111": true, "111111111111": true,
	"p-custom111": true, "rp-abcd1234": true, "rt-out11111": true, "rt-in111111": true, "rt-gov11111": true,
}

// orgCalls records what a scan sent: the ResourceIds of the tag reads, and
// the MaxResults of every listing call. tagErrs (set before the scan) are
// the errors the tag reads of those ResourceIds return.
type orgCalls struct {
	mu      sync.Mutex
	tagIDs  []string
	caps    map[string][]int32
	tagErrs map[string]error
}

func newOrgCalls() *orgCalls {
	return &orgCalls{caps: map[string][]int32{}, tagErrs: map[string]error{}}
}

func (c *orgCalls) listed(op string, maxResults *int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.caps[op] = append(c.caps[op], sdkaws.ToInt32(maxResults))
}

func (c *orgCalls) listTags(in any) (any, error) {
	id := sv(in.(*organizations.ListTagsForResourceInput).ResourceId)
	c.mu.Lock()
	c.tagIDs = append(c.tagIDs, id)
	err := c.tagErrs[id]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !orgTestTagged[id] {
		return &organizations.ListTagsForResourceOutput{}, nil
	}
	return &organizations.ListTagsForResourceOutput{Tags: []organizationstypes.Tag{{Key: sdkaws.String("id"), Value: sdkaws.String(id)}}}, nil
}

func (c *orgCalls) sortedTagIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagIDs))
}

// orgTreeHandlers answers a management-account scan of a two-root org. Root
// r-ab12 holds one OU and account 111111111111; that OU holds 222222222222.
// Root r-noarn is listed without an Arn; it holds an OU and account
// 333333333333, and its OU holds 444444444444. No SCP is listed.
func orgTreeHandlers() map[string]func(in any) (any, error) {
	return newOrgCalls().treeHandlers()
}

func (c *orgCalls) treeHandlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeOrganization": func(any) (any, error) {
			return &organizations.DescribeOrganizationOutput{Organization: &organizationstypes.Organization{
				Id: sdkaws.String("o-abc"), Arn: sdkaws.String(orgTestOrgARN), MasterAccountId: sdkaws.String(testAccountID),
			}}, nil
		},
		"ListRoots": func(in any) (any, error) {
			c.listed("ListRoots", in.(*organizations.ListRootsInput).MaxResults)
			return &organizations.ListRootsOutput{Roots: []organizationstypes.Root{
				{
					Id: sdkaws.String("r-ab12"), Arn: sdkaws.String(orgTestRootARN), Name: sdkaws.String("Root"),
					PolicyTypes: []organizationstypes.PolicyTypeSummary{{Type: organizationstypes.PolicyTypeServiceControlPolicy, Status: organizationstypes.PolicyTypeStatusEnabled}},
				},
				{Id: sdkaws.String("r-noarn"), Name: sdkaws.String("NoArn")},
			}}, nil
		},
		"ListOrganizationalUnitsForParent": func(in any) (any, error) {
			req := in.(*organizations.ListOrganizationalUnitsForParentInput)
			c.listed("ListOrganizationalUnitsForParent", req.MaxResults)
			switch sv(req.ParentId) {
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
		"ListAccounts": func(in any) (any, error) {
			c.listed("ListAccounts", in.(*organizations.ListAccountsInput).MaxResults)
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
		"ListPolicies": func(in any) (any, error) {
			c.listed("ListPolicies", in.(*organizations.ListPoliciesInput).MaxResults)
			return &organizations.ListPoliciesOutput{}, nil
		},
		"ListTagsForResource": c.listTags,
	}
}

func orgTestPolicySummary(id, arn, name string, awsManaged bool) organizationstypes.PolicySummary {
	return organizationstypes.PolicySummary{
		Id: sdkaws.String(id), Arn: sdkaws.String(arn), Name: sdkaws.String(name),
		Type: organizationstypes.PolicyTypeServiceControlPolicy, AwsManaged: awsManaged,
	}
}

// orgFullHandlers is treeHandlers plus one row of every other type: OU
// ou-ab12-33333333 under ou-ab12-11111111, a customer SCP and two
// AWS-managed ones (FullAWSAccess and p-awsmanaged1), the resource policy,
// and an outbound and an inbound transfer.
func (c *orgCalls) fullHandlers() map[string]func(in any) (any, error) {
	h := c.treeHandlers()
	ous := h["ListOrganizationalUnitsForParent"]
	h["ListOrganizationalUnitsForParent"] = func(in any) (any, error) {
		if sv(in.(*organizations.ListOrganizationalUnitsForParentInput).ParentId) == "ou-ab12-11111111" {
			c.listed("ListOrganizationalUnitsForParent", in.(*organizations.ListOrganizationalUnitsForParentInput).MaxResults)
			return &organizations.ListOrganizationalUnitsForParentOutput{OrganizationalUnits: []organizationstypes.OrganizationalUnit{{
				Id: sdkaws.String("ou-ab12-33333333"), Arn: sdkaws.String(orgTestDeepOUARN), Name: sdkaws.String("Deep"),
			}}}, nil
		}
		return ous(in)
	}
	policies := map[string]organizationstypes.PolicySummary{
		"p-custom111":     orgTestPolicySummary("p-custom111", orgTestCustomSCPARN, "DenyLeave", false),
		"p-FullAWSAccess": orgTestPolicySummary("p-FullAWSAccess", orgTestFullAccessARN, "FullAWSAccess", true),
		"p-awsmanaged1":   orgTestPolicySummary("p-awsmanaged1", orgTestAWSManagedSCPARN, "AWSManagedOther", true),
	}
	h["ListPolicies"] = func(in any) (any, error) {
		req := in.(*organizations.ListPoliciesInput)
		c.listed("ListPolicies", req.MaxResults)
		if req.Filter != organizationstypes.PolicyTypeServiceControlPolicy {
			return nil, apiErr("InvalidInputException", "unexpected filter "+string(req.Filter))
		}
		return &organizations.ListPoliciesOutput{Policies: []organizationstypes.PolicySummary{policies["p-custom111"], policies["p-FullAWSAccess"], policies["p-awsmanaged1"]}}, nil
	}
	h["DescribePolicy"] = func(in any) (any, error) {
		s := policies[sv(in.(*organizations.DescribePolicyInput).PolicyId)]
		return &organizations.DescribePolicyOutput{Policy: &organizationstypes.Policy{PolicySummary: &s, Content: sdkaws.String(`{"Version":"2012-10-17","Id":"` + sv(s.Id) + `"}`)}}, nil
	}
	h["DescribeResourcePolicy"] = func(any) (any, error) {
		return &organizations.DescribeResourcePolicyOutput{ResourcePolicy: &organizationstypes.ResourcePolicy{
			ResourcePolicySummary: &organizationstypes.ResourcePolicySummary{Id: sdkaws.String("rp-abcd1234"), Arn: sdkaws.String(orgTestRPARN)},
			Content:               sdkaws.String(`{"Version":"2012-10-17"}`),
		}}, nil
	}
	h["ListInboundResponsibilityTransfers"] = func(in any) (any, error) {
		c.listed("ListInboundResponsibilityTransfers", in.(*organizations.ListInboundResponsibilityTransfersInput).MaxResults)
		return &organizations.ListInboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{{
			Id: sdkaws.String("rt-in111111"), Arn: sdkaws.String(orgTestInARN), Name: sdkaws.String("inbound"),
		}}}, nil
	}
	h["ListOutboundResponsibilityTransfers"] = func(in any) (any, error) {
		c.listed("ListOutboundResponsibilityTransfers", in.(*organizations.ListOutboundResponsibilityTransfersInput).MaxResults)
		return &organizations.ListOutboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{{
			Id: sdkaws.String("rt-out11111"), Arn: sdkaws.String(orgTestOutARN), Name: sdkaws.String("outbound"),
		}}}, nil
	}
	return h
}

func orgScanWith(st *store.Store, h map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, orgTestDenied)
	return scanOrganizations(context.Background(), acct, "", st, testScanID)
}

// TestScanOrganizations_RootStoredOnceTypedRoot pins that the tree phase stores
// each root once, typed root, and that OUs and accounts still hang under it.
// Storing it a second time under another type in the same scan merges both
// into one row whose type flips every scan, splitting a version each time.
func TestScanOrganizations_RootStoredOnceTypedRoot(t *testing.T) {
	st := newTestStore(t)
	total, _, err := orgScanWith(st, orgTreeHandlers())
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
	if _, _, err := orgScanWith(st, orgTreeHandlers()); err != nil {
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
	// Account 333333333333's parent is the rowless root: it is stored
	// unlinked and warned about, not dropped from the hierarchy silently.
	if n := warns["aws/organizations:ListParents"]; n != 1 {
		t.Errorf("organizations:ListParents warnings = %d, want 1 for the account under the ARN-less root", n)
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
// unlinked and warned about once; any other error fails the scan. Where
// ListParents answers, account 333333333333 (under the ARN-less root, which
// is not stored) adds one more warning.
func TestScanOrganizations_ListParentsErrors(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "organizations:ListParents")
		h := orgTreeHandlers()
		h["ListParents"] = func(any) (any, error) {
			return nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized to perform: organizations:ListParents"}
		}
		if _, _, err := orgScanWith(st, h); err != nil {
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
		all := recordWarnings(st)
		warns := countOpWarnings(st, "organizations:ListParents")
		h := orgTreeHandlers()
		parents := h["ListParents"]
		h["ListParents"] = func(in any) (any, error) {
			if sv(in.(*organizations.ListParentsInput).ChildId) == "111111111111" {
				return nil, &smithy.GenericAPIError{Code: "ChildNotFoundException", Message: "gone"}
			}
			return parents(in)
		}
		total, _, err := orgScanWith(st, h)
		if err != nil || *warns != 2 || total != 8 {
			t.Fatalf("total = %d, warnings = %d, err = %v; want 8 rows, 2 warnings (gone account, ARN-less root's account), nil", total, *warns, err)
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
		// Scoped like the scanner's other "stored" warnings.
		for _, w := range *all {
			if w.Service == "organizations:ListParents" && w.Scope != testAccountID+"/global" {
				t.Errorf("ListParents warning Scope = %q; want %s/global", w.Scope, testAccountID)
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
		{"throttled", &smithy.GenericAPIError{Code: "TooManyRequestsException", Message: "slow down"}, 2, ""},
		{"other error", &smithy.GenericAPIError{Code: "InvalidInputException", Message: "bad"}, 1, "InvalidInputException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:ListParents")
			_, _, err := orgScanWith(st, oneFails(tc.err))
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
			// The closure is recorded whatever the account phase returned.
			assertContainsNative(t, st, orgTestOUARN, orgTestAcct2ARN)
		})
	}
	// A later page's failed write warns nothing, not even about the accounts
	// of the pages written before it.
	t.Run("denied, later write fails", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		warns := countOpWarnings(st, "organizations:ListParents")
		h := orgTreeHandlers()
		accounts := h["ListAccounts"]
		h["ListAccounts"] = func(in any) (any, error) {
			out, err := accounts(in)
			page := out.(*organizations.ListAccountsOutput)
			if in.(*organizations.ListAccountsInput).NextToken == nil {
				page.Accounts, page.NextToken = page.Accounts[:2], sdkaws.String("t2")
				return page, err
			}
			closeStore()
			page.Accounts = page.Accounts[2:]
			return page, err
		}
		h["ListParents"] = func(any) (any, error) {
			return nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized to perform: organizations:ListParents"}
		}
		_, _, err := orgScanWith(st, h)
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Errorf("err = %v, want the store write failure", err)
		}
		if *warns != 0 {
			t.Errorf("organizations:ListParents warnings = %d, want 0 after a failed write", *warns)
		}
	})
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
		_, _, err := orgScanWith(st, h)
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
		{"no organization", &smithy.GenericAPIError{Code: "AWSOrganizationsNotInUseException", Message: "none"}, 0, ""},
		{"other error", &smithy.GenericAPIError{Code: "ServiceException", Message: "boom"}, 0, "ServiceException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:DescribeResourcePolicy")
			h := orgTreeHandlers()
			h["DescribeResourcePolicy"] = func(any) (any, error) { return nil, tc.err }
			_, _, err := orgScanWith(st, h)
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

func orgTestTransfer(id string) organizationstypes.ResponsibilityTransfer {
	return organizationstypes.ResponsibilityTransfer{
		Id: sdkaws.String(id), Arn: sdkaws.String("arn:aws:organizations::" + testAccountID + ":transfer/o-abc/billing/outbound/" + id),
	}
}

// A failed phase costs no other phase its rows: the later phases still run,
// and the closure links what the tree phases stored.
func TestScanOrganizations_PhaseFailureKeepsOtherPhases(t *testing.T) {
	for _, op := range []string{"ListOrganizationalUnitsForParent", "ListAccounts", "ListPolicies"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			h := orgTreeHandlers()
			h[op] = func(any) (any, error) {
				return nil, &smithy.GenericAPIError{Code: "InvalidInputException", Message: "bad"}
			}
			h["ListInboundResponsibilityTransfers"] = func(any) (any, error) {
				return &organizations.ListInboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{orgTestTransfer("rt-1")}}, nil
			}
			_, _, err := orgScanWith(st, h)
			assertAPICode(t, err, "InvalidInputException")
			if n := len(rowsByNativeID(t, st, TypeOrganizationsResponsibilityTransfer)); n != 1 {
				t.Errorf("responsibility transfer rows = %d; want 1 despite %s failing", n, op)
			}
			assertContainsNative(t, st, orgTestOrgARN, orgTestRootARN)
			if op != "ListAccounts" {
				assertContainsNative(t, st, orgTestRootARN, orgTestAcct1ARN)
			}
		})
	}
}

// orgTestTransferScan runs the transfer phase alone and returns its counts
// and joined errors.
func orgTestTransferScan(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	scanOrgResponsibilityTransfers(context.Background(), newOrgScan(organizations.NewFromConfig(opStubConfig(h, orgTestDenied)), acct, st, testScanID, orgTestOrgARN), &p)
	total, _, err := p.result(st, acct, "")
	return total, err
}

// A list that repeats its token keeps the transfers read, the other list
// still runs, and the listing fails as incomplete.
func TestScanOrgResponsibilityTransfers_RepeatedTokenKeepsRows(t *testing.T) {
	st := newTestStore(t)
	var inCalls int
	calls := newOrgCalls()
	total, err := orgTestTransferScan(t, st, map[string]func(in any) (any, error){
		"ListInboundResponsibilityTransfers": func(any) (any, error) {
			inCalls++
			if inCalls > 2 {
				return nil, &smithy.GenericAPIError{Code: "ServiceException", Message: "called past the repeated token"}
			}
			return &organizations.ListInboundResponsibilityTransfersOutput{
				ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{orgTestTransfer([]string{"rt-in-1", "rt-in-2"}[inCalls-1])},
				NextToken:               sdkaws.String("same"),
			}, nil
		},
		"ListOutboundResponsibilityTransfers": func(any) (any, error) {
			return &organizations.ListOutboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{orgTestTransfer("rt-out")}}, nil
		},
		"ListTagsForResource": calls.listTags,
	})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v; want errRepeatedNextToken", err)
	}
	if total != 3 || inCalls != 2 {
		t.Errorf("total = %d, inbound calls = %d; want 3 rows (2 inbound, 1 outbound) from 2 inbound calls", total, inCalls)
	}
	if n := len(rowsByNativeID(t, st, TypeOrganizationsResponsibilityTransfer)); n != 3 {
		t.Errorf("rows = %d; want 3", n)
	}
}

// A list that fails keeps the other list's transfers, and the failure is
// returned after they are stored. A denied list is warned about under its
// own op instead.
func TestScanOrgResponsibilityTransfers_FailedListKeepsOtherList(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{"other error", apiErr("ServiceException", "boom"), "ServiceException", 0},
		{"denied", apiErr("AccessDeniedException", "not authorized to perform: organizations:ListInboundResponsibilityTransfers"), "", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:ListInboundResponsibilityTransfers")
			calls := newOrgCalls()
			total, err := orgTestTransferScan(t, st, map[string]func(in any) (any, error){
				"ListInboundResponsibilityTransfers": func(any) (any, error) { return nil, c.err },
				"ListOutboundResponsibilityTransfers": func(any) (any, error) {
					return &organizations.ListOutboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{orgTestTransfer("rt-out")}}, nil
				},
				"ListTagsForResource": calls.listTags,
			})
			if c.wantCode != "" {
				assertAPICode(t, err, c.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != c.wantWarns {
				t.Errorf("ListInboundResponsibilityTransfers warnings = %d; want %d", *warns, c.wantWarns)
			}
			if total != 1 {
				t.Errorf("total = %d; want 1 outbound transfer", total)
			}
			if n := len(rowsByNativeID(t, st, TypeOrganizationsResponsibilityTransfer)); n != 1 {
				t.Errorf("rows = %d; want 1", n)
			}
		})
	}
}

// A transfer listed without an Arn is not stored, with or without an Id, and
// is warned about once under the list that returned it; one listed by both
// lists is stored once, silently.
func TestScanOrgResponsibilityTransfers_ARNlessWarnedUnderItsList(t *testing.T) {
	st := newTestStore(t)
	inWarns := countOpWarnings(st, "organizations:ListInboundResponsibilityTransfers")
	outWarns := countOpWarnings(st, "organizations:ListOutboundResponsibilityTransfers")
	calls := newOrgCalls()
	both := orgTestTransfer("rt-both111")
	total, err := orgTestTransferScan(t, st, map[string]func(in any) (any, error){
		"ListInboundResponsibilityTransfers": func(any) (any, error) {
			return &organizations.ListInboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{
				both, {Id: sdkaws.String("rt-noarn11"), Name: sdkaws.String("id only")}, {Name: sdkaws.String("keyless")},
			}}, nil
		},
		"ListOutboundResponsibilityTransfers": func(any) (any, error) {
			return &organizations.ListOutboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{
				both, {Id: sdkaws.String("rt-noarn22")},
			}}, nil
		},
		"ListTagsForResource": calls.listTags,
	})
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if *inWarns != 1 || *outWarns != 1 {
		t.Errorf("inbound, outbound warnings = %d, %d; want 1, 1", *inWarns, *outWarns)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeOrganizationsResponsibilityTransfer)); !slices.Equal(got, []string{sv(both.Arn)}) {
		t.Errorf("rows = %v; want only %s", got, sv(both.Arn))
	}
	if got := calls.sortedTagIDs(); !slices.Equal(got, []string{"rt-both111"}) {
		t.Errorf("tag reads = %v; want one for rt-both111", got)
	}
}

// A failed write of the transfers sends neither list's warning: not the
// inbound list's denial, nor the outbound list's ARN-less transfer.
func TestScanOrgResponsibilityTransfers_FailedWriteSendsNoDenialWarning(t *testing.T) {
	st, closeStore := openClosingStore(t)
	inWarns := countOpWarnings(st, "organizations:ListInboundResponsibilityTransfers")
	outWarns := countOpWarnings(st, "organizations:ListOutboundResponsibilityTransfers")
	_, err := orgTestTransferScan(t, st, map[string]func(in any) (any, error){
		"ListInboundResponsibilityTransfers": func(any) (any, error) {
			return nil, apiErr("AccessDeniedException", "not authorized to perform: organizations:ListInboundResponsibilityTransfers")
		},
		"ListOutboundResponsibilityTransfers": func(any) (any, error) {
			closeStore()
			return &organizations.ListOutboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{
				orgTestTransfer("rt-out"), {Id: sdkaws.String("rt-noarn11")},
			}}, nil
		},
		"ListTagsForResource": newOrgCalls().listTags,
	})
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want the store write failure", err)
	}
	if *inWarns != 0 || *outWarns != 0 {
		t.Errorf("inbound, outbound warnings = %d, %d; want 0, 0 after a failed write", *inWarns, *outWarns)
	}
}

// A throttled transfer list beside another phase's hard error is demoted to
// one warning under the list that failed.
func TestScanOrganizations_TransferListThrottleDemotedUnderItsOp(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "organizations:ListInboundResponsibilityTransfers")
	h := newOrgCalls().fullHandlers()
	h["ListInboundResponsibilityTransfers"] = func(any) (any, error) {
		return nil, apiErr("TooManyRequestsException", "slow down")
	}
	h["ListPolicies"] = func(any) (any, error) { return nil, apiErr("InvalidInputException", "bad") }
	_, _, err := orgScanWith(st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("ListInboundResponsibilityTransfers warnings = %d; want 1", *warns)
	}
}

// Every type but the organization has its tags read with
// ListTagsForResource, sent the element's Id; the AWS-managed policies
// (AwsManaged, whatever their Id) are flagged ManagedByProvider and not read. Every listing sends
// MaxResults 20, and the rows keep their fields.
func TestScanOrganizations_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	tagWarns := countOpWarnings(st, "organizations:ListTagsForResource")
	descWarns := countOpWarnings(st, "organizations:DescribePolicy")
	calls := newOrgCalls()
	h := calls.fullHandlers()
	listAccounts := h["ListAccounts"]
	joined := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	h["ListAccounts"] = func(in any) (any, error) {
		out, err := listAccounts(in)
		accounts := out.(*organizations.ListAccountsOutput).Accounts
		accounts[0].Status, accounts[0].JoinedTimestamp = organizationstypes.AccountStatusActive, &joined
		return out, err
	}

	total, inserted, err := orgScanWith(st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != orgTestFullTotal || inserted != orgTestFullTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, orgTestFullTotal, orgTestFullTotal)
	}
	if *tagWarns != 0 || *descWarns != 0 {
		t.Errorf("tag, DescribePolicy warnings = %d, %d; want none", *tagWarns, *descWarns)
	}
	wantIDs := []string{
		"111111111111", "222222222222", "333333333333", "444444444444",
		"ou-ab12-11111111", "ou-ab12-33333333", "ou-noar-22222222",
		"p-custom111", "r-ab12", "rp-abcd1234", "rt-in111111", "rt-out11111",
	}
	if got := calls.sortedTagIDs(); !slices.Equal(got, wantIDs) {
		t.Errorf("ListTagsForResource ResourceIds = %v\nwant %v", got, wantIDs)
	}
	for _, c := range []struct {
		typ, arn, id string
	}{
		{TypeOrganizationsRoot, orgTestRootARN, "r-ab12"},
		{TypeOrganizationsOU, orgTestOUARN, "ou-ab12-11111111"},
		{TypeOrganizationsOU, orgTestDeepOUARN, ""},
		{TypeOrganizationsAccount, orgTestAcct1ARN, "111111111111"},
		{TypeOrganizationsAccount, orgTestAcct2ARN, ""},
		{TypeOrganizationsSCP, orgTestCustomSCPARN, "p-custom111"},
		{TypeOrganizationsSCP, orgTestFullAccessARN, ""},
		{TypeOrganizationsSCP, orgTestAWSManagedSCPARN, ""},
		{TypeOrganizationsResourcePolicy, orgTestRPARN, "rp-abcd1234"},
		{TypeOrganizationsResponsibilityTransfer, orgTestOutARN, "rt-out11111"},
		{TypeOrganizationsResponsibilityTransfer, orgTestInARN, "rt-in111111"},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok {
			t.Errorf("%s %s not stored", c.typ, c.arn)
			continue
		}
		if sv(r.Region) != "global" {
			t.Errorf("%s Region = %q; want global", c.arn, sv(r.Region))
		}
		if c.id == "" {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", c.arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"id": c.id})
	}

	scps := rowsByNativeID(t, st, TypeOrganizationsSCP)
	for arn, want := range map[string]struct {
		name, id string
		managed  bool
	}{
		orgTestCustomSCPARN:     {"DenyLeave", "p-custom111", false},
		orgTestFullAccessARN:    {"FullAWSAccess", "p-FullAWSAccess", true},
		orgTestAWSManagedSCPARN: {"AWSManagedOther", "p-awsmanaged1", true},
	} {
		r := scps[arn]
		var attrs organizationstypes.Policy
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.PolicySummary == nil ||
			sv(attrs.PolicySummary.Id) != want.id || sv(attrs.Content) != `{"Version":"2012-10-17","Id":"`+want.id+`"}` {
			t.Errorf("%s attrs = %s (%v); want the DescribePolicy body", arn, r.AttributesJSON, err)
		}
		if sv(r.Name) != want.name || r.ManagedByProvider != want.managed || r.Status != nil || r.CreatedAt != nil {
			t.Errorf("%s Name, ManagedByProvider, Status, CreatedAt = %q, %v, %v, %v; want %q, %v, nil, nil", arn, sv(r.Name), r.ManagedByProvider, r.Status, r.CreatedAt, want.name, want.managed)
		}
	}
	acct := rowsByNativeID(t, st, TypeOrganizationsAccount)[orgTestAcct1ARN]
	if sv(acct.Name) != "one" || sv(acct.Status) != "ACTIVE" || sv(acct.CreatedAt) != "2024-03-04T05:06:07Z" {
		t.Errorf("account Name, Status, CreatedAt = %q, %q, %q; want one, ACTIVE, 2024-03-04T05:06:07Z", sv(acct.Name), sv(acct.Status), sv(acct.CreatedAt))
	}
	// loadOrgTargetIndex keys on these attributes.
	var acctAttrs organizationstypes.Account
	if err := json.Unmarshal([]byte(acct.AttributesJSON), &acctAttrs); err != nil || sv(acctAttrs.Id) != "111111111111" || sv(acctAttrs.Arn) != orgTestAcct1ARN {
		t.Errorf("account attrs = %s (%v); want the listed account", acct.AttributesJSON, err)
	}
	ou := rowsByNativeID(t, st, TypeOrganizationsOU)[orgTestDeepOUARN]
	var ouAttrs organizationstypes.OrganizationalUnit
	if err := json.Unmarshal([]byte(ou.AttributesJSON), &ouAttrs); err != nil || sv(ouAttrs.Id) != "ou-ab12-33333333" || sv(ou.Name) != "Deep" {
		t.Errorf("deep OU Name, attrs = %q, %s (%v); want Deep and the listed OU", sv(ou.Name), ou.AttributesJSON, err)
	}
	assertContainsNative(t, st, orgTestOUARN, orgTestDeepOUARN)
	rp := rowsByNativeID(t, st, TypeOrganizationsResourcePolicy)[orgTestRPARN]
	var rpAttrs organizationstypes.ResourcePolicy
	if err := json.Unmarshal([]byte(rp.AttributesJSON), &rpAttrs); err != nil || sv(rpAttrs.Content) != `{"Version":"2012-10-17"}` ||
		rpAttrs.ResourcePolicySummary == nil || sv(rpAttrs.ResourcePolicySummary.Id) != "rp-abcd1234" {
		t.Errorf("resource policy attrs = %s (%v); want the DescribeResourcePolicy body", rp.AttributesJSON, err)
	}
	if sv(rp.Name) != testAccountID {
		t.Errorf("resource policy Name = %q; want %s", sv(rp.Name), testAccountID)
	}
	transfers := rowsByNativeID(t, st, TypeOrganizationsResponsibilityTransfer)
	for arn, want := range map[string]struct{ id, name string }{
		orgTestInARN:  {"rt-in111111", "inbound"},
		orgTestOutARN: {"rt-out11111", "outbound"},
	} {
		r := transfers[arn]
		var attrs organizationstypes.ResponsibilityTransfer
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.Id) != want.id || sv(attrs.Arn) != arn || sv(attrs.Name) != want.name {
			t.Errorf("transfer %s attrs = %s (%v); want the listed transfer", arn, r.AttributesJSON, err)
		}
		if sv(r.Name) != want.name {
			t.Errorf("transfer %s Name = %q; want %q", arn, sv(r.Name), want.name)
		}
	}
	for _, op := range []string{
		"ListRoots", "ListOrganizationalUnitsForParent", "ListAccounts", "ListPolicies",
		"ListInboundResponsibilityTransfers", "ListOutboundResponsibilityTransfers",
	} {
		caps := calls.caps[op]
		if len(caps) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range caps {
			if n != 20 {
				t.Errorf("%s MaxResults = %d; want 20", op, n)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanOrganizations_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := orgScanWith(st, newOrgCalls().fullHandlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := orgScanWith(st, newOrgCalls().fullHandlers())
	if err != nil || total != orgTestFullTotal || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want %d, 0, nil", total, inserted, err, orgTestFullTotal)
	}
}

// A failed tag read. TargetNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (an OU with it: its child
// OU is not listed, its account is stored unlinked). A denial keeps the row
// untagged and warns once under the tag op; a throttle keeps it and is
// transient; any other error keeps it and is an error.
func TestScanOrganizations_TagReadFailures(t *testing.T) {
	gone := apiErr("TargetNotFoundException", "gone")
	for _, c := range []struct {
		name      string
		id, arn   string
		typ       string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"account/not found", "111111111111", orgTestAcct1ARN, TypeOrganizationsAccount, gone, -1, "", orgTestFullTotal - 1, false, false},
		{"OU/not found", "ou-ab12-11111111", orgTestOUARN, TypeOrganizationsOU, gone, -1, "", orgTestFullTotal - 2, false, false},
		{"policy/not found", "p-custom111", orgTestCustomSCPARN, TypeOrganizationsSCP, gone, -1, "", orgTestFullTotal - 1, false, false},
		{"own transfer/not found", "rt-out11111", orgTestOutARN, TypeOrganizationsResponsibilityTransfer, gone, -1, "", orgTestFullTotal - 1, false, false},
		{"account/denied", "111111111111", orgTestAcct1ARN, TypeOrganizationsAccount, orgTestDenied, -1, "", orgTestFullTotal, true, true},
		{"resource policy/denied", "rp-abcd1234", orgTestRPARN, TypeOrganizationsResourcePolicy, orgTestDenied, -1, "", orgTestFullTotal, true, true},
		{"own transfer/denied", "rt-out11111", orgTestOutARN, TypeOrganizationsResponsibilityTransfer, orgTestDenied, -1, "", orgTestFullTotal, true, true},
		// The inbound transfer's ARN names another account: kept untagged,
		// silently, whether denied or not found.
		{"other account's transfer/denied", "rt-in111111", orgTestInARN, TypeOrganizationsResponsibilityTransfer, orgTestDenied, -1, "", orgTestFullTotal, true, false},
		{"other account's transfer/not found", "rt-in111111", orgTestInARN, TypeOrganizationsResponsibilityTransfer, gone, -1, "", orgTestFullTotal, true, false},
		{"root/transient", "r-ab12", orgTestRootARN, TypeOrganizationsRoot, apiErr("TooManyRequestsException", "slow down"), outcomeTransient, "TooManyRequestsException", orgTestFullTotal, true, false},
		{"root/other error", "r-ab12", orgTestRootARN, TypeOrganizationsRoot, apiErr("InvalidInputException", "bad"), outcomeError, "InvalidInputException", orgTestFullTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:ListTagsForResource")
			calls := newOrgCalls()
			calls.tagErrs[c.id] = c.err

			total, _, err := orgScanWith(st, calls.fullHandlers())
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			want := 0
			if c.warn {
				want = 1
			}
			if *warns != want {
				t.Errorf("tag warnings = %d; want %d", *warns, want)
			}
		})
	}
}

// ListTagsForResource pages are all read; a repeated NextToken fails the
// read, keeping the row untagged.
func TestScanOrganizations_TagPages(t *testing.T) {
	pageOf := func(next *string, k, v string) *organizations.ListTagsForResourceOutput {
		return &organizations.ListTagsForResourceOutput{NextToken: next, Tags: []organizationstypes.Tag{{Key: sdkaws.String(k), Value: sdkaws.String(v)}}}
	}
	for _, c := range []struct {
		name     string
		second   *string
		wantTags map[string]string
	}{
		{"every page read", nil, map[string]string{"a": "1", "b": "2"}},
		{"repeated token", sdkaws.String("t2"), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newOrgCalls()
			h := calls.fullHandlers()
			h["ListTagsForResource"] = func(in any) (any, error) {
				req := in.(*organizations.ListTagsForResourceInput)
				if sv(req.ResourceId) != "222222222222" {
					return calls.listTags(in)
				}
				if req.NextToken == nil {
					return pageOf(sdkaws.String("t2"), "a", "1"), nil
				}
				return pageOf(c.second, "b", "2"), nil
			}
			total, _, err := orgScanWith(st, h)
			if total != orgTestFullTotal {
				t.Errorf("total = %d; want %d", total, orgTestFullTotal)
			}
			r := rowsByNativeID(t, st, TypeOrganizationsAccount)[orgTestAcct2ARN]
			if c.wantTags == nil {
				if !errors.Is(err, errRepeatedNextToken) {
					t.Fatalf("err = %v; want errRepeatedNextToken", err)
				}
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				return
			}
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			assertTagsMap(t, r.TagsJSON, c.wantTags)
		})
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanOrganizations_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "organizations:ListTagsForResource")
	calls := newOrgCalls()
	for _, id := range []string{"r-ab12", "ou-ab12-33333333", "222222222222", "p-custom111", "rt-out11111"} {
		calls.tagErrs[id] = orgTestDenied
	}
	total, _, err := orgScanWith(st, calls.fullHandlers())
	if err != nil || total != orgTestFullTotal {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, orgTestFullTotal)
	}
	if *warns != 1 {
		t.Errorf("tag warnings = %d; want 1", *warns)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanOrganizations_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "organizations:ListTagsForResource")
	calls := newOrgCalls()
	calls.tagErrs["ou-ab12-11111111"] = apiErr("TooManyRequestsException", "slow down")
	calls.tagErrs["p-custom111"] = apiErr("InvalidInputException", "bad")

	_, _, err := orgScanWith(st, calls.fullHandlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("tag warnings = %d; want 1", *warns)
	}
}

// A denied tag read whose rows a failed write lost warns nothing, and the
// failed write stops the scan: no later phase runs.
func TestScanOrganizations_FailedWriteStopsScan(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, "organizations:ListTagsForResource")
	calls := newOrgCalls()
	h := calls.fullHandlers()
	listPolicies := h["ListPolicies"]
	policiesCalled := false
	h["ListPolicies"] = func(in any) (any, error) {
		policiesCalled = true
		return listPolicies(in)
	}
	h["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*organizations.ListTagsForResourceInput).ResourceId) == "111111111111" {
			closeStore()
			return nil, orgTestDenied
		}
		return calls.listTags(in)
	}
	_, _, err := orgScanWith(st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", err)
	}
	if *warns != 0 {
		t.Errorf("tag warnings = %d; want 0", *warns)
	}
	if policiesCalled {
		t.Error("ListPolicies called after a failed write")
	}
}

// DescribePolicy: a policy it no longer finds (PolicyNotFoundException, its
// modeled not-found) is not stored; a denial keeps the listed summary as the
// row, tags read, with one warning; any other error keeps it and is
// returned; an answer without a Policy keeps it silently.
func TestScanOrganizations_DescribePolicyFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantKept  bool
		wantWarns int
		wantCode  string
	}{
		{"not found", apiErr("PolicyNotFoundException", "gone"), false, 0, ""},
		{"denied", orgTestDenied, true, 1, ""},
		{"other error", apiErr("InvalidInputException", "bad"), true, 0, "InvalidInputException"},
		{"no policy in the answer", nil, true, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:DescribePolicy")
			calls := newOrgCalls()
			h := calls.fullHandlers()
			describe := h["DescribePolicy"]
			h["DescribePolicy"] = func(in any) (any, error) {
				if sv(in.(*organizations.DescribePolicyInput).PolicyId) != "p-custom111" {
					return describe(in)
				}
				if c.err == nil {
					return &organizations.DescribePolicyOutput{}, nil
				}
				return nil, c.err
			}
			total, _, err := orgScanWith(st, h)
			if c.wantCode != "" {
				assertAPICode(t, err, c.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != c.wantWarns {
				t.Errorf("DescribePolicy warnings = %d; want %d", *warns, c.wantWarns)
			}
			r, ok := rowsByNativeID(t, st, TypeOrganizationsSCP)[orgTestCustomSCPARN]
			if ok != c.wantKept {
				t.Fatalf("policy stored = %v; want %v", ok, c.wantKept)
			}
			wantTotal := orgTestFullTotal
			if !ok {
				wantTotal--
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
			if !ok {
				if slices.Contains(calls.sortedTagIDs(), "p-custom111") {
					t.Error("tags read for a policy DescribePolicy no longer finds")
				}
				return
			}
			var summary organizationstypes.PolicySummary
			if err := json.Unmarshal([]byte(r.AttributesJSON), &summary); err != nil || sv(summary.Id) != "p-custom111" || sv(r.Name) != "DenyLeave" {
				t.Errorf("attrs, Name = %s, %q (%v); want the listed summary and DenyLeave", r.AttributesJSON, sv(r.Name), err)
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"id": "p-custom111"})
		})
	}
}

// The OU walk: one parent's failed listing keeps the pages read before it
// (and walks the OUs on them) and costs the other parents nothing; the
// failure is returned. A parent deleted since it was listed
// (ParentNotFoundException, modeled) is skipped silently.
func TestScanOrganizations_OUWalkParentFailures(t *testing.T) {
	for _, c := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{"later page fails", apiErr("InvalidInputException", "bad"), "InvalidInputException"},
		{"parent gone", apiErr("ParentNotFoundException", "gone"), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:ListOrganizationalUnitsForParent")
			h := newOrgCalls().fullHandlers()
			ous := h["ListOrganizationalUnitsForParent"]
			h["ListOrganizationalUnitsForParent"] = func(in any) (any, error) {
				req := in.(*organizations.ListOrganizationalUnitsForParentInput)
				switch {
				case sv(req.ParentId) != "r-ab12":
					return ous(in)
				case c.wantCode == "":
					return nil, c.err
				case req.NextToken == nil:
					out, err := ous(in)
					out.(*organizations.ListOrganizationalUnitsForParentOutput).NextToken = sdkaws.String("t2")
					return out, err
				}
				return nil, c.err
			}
			_, _, err := orgScanWith(st, h)
			if c.wantCode != "" {
				assertAPICode(t, err, c.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 0 {
				t.Errorf("OU listing warnings = %d; want 0", *warns)
			}
			ouRows := rowsByNativeID(t, st, TypeOrganizationsOU)
			want := []string{orgTestNoARNOU}
			if c.wantCode != "" {
				want = []string{orgTestOUARN, orgTestDeepOUARN, orgTestNoARNOU}
			}
			if got := nativeIDs(ouRows); !slices.Equal(got, want) {
				t.Errorf("OU rows = %v; want %v", got, want)
			}
		})
	}
}

// A later ListAccounts page failing keeps the accounts of the pages before
// it, linked; a repeated NextToken ends the listing as incomplete.
func TestScanOrganizations_AccountPages(t *testing.T) {
	for _, c := range []struct {
		name    string
		second  func() (any, error)
		wantErr func(t *testing.T, err error)
	}{
		{"later page fails", func() (any, error) { return nil, apiErr("InvalidInputException", "bad") }, func(t *testing.T, err error) {
			assertAPICode(t, err, "InvalidInputException")
		}},
		{"repeated token", func() (any, error) {
			return &organizations.ListAccountsOutput{NextToken: sdkaws.String("t2")}, nil
		}, func(t *testing.T, err error) {
			if !errors.Is(err, errRepeatedNextToken) {
				t.Errorf("err = %v; want errRepeatedNextToken", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := orgTreeHandlers()
			accounts := h["ListAccounts"]
			h["ListAccounts"] = func(in any) (any, error) {
				if in.(*organizations.ListAccountsInput).NextToken != nil {
					return c.second()
				}
				out, err := accounts(in)
				page := out.(*organizations.ListAccountsOutput)
				page.Accounts, page.NextToken = page.Accounts[:2], sdkaws.String("t2")
				return page, err
			}
			total, _, err := orgScanWith(st, h)
			c.wantErr(t, err)
			// organization + root + two OUs + the first page's two accounts.
			if total != 6 {
				t.Errorf("total = %d; want 6", total)
			}
			assertContainsNative(t, st, orgTestRootARN, orgTestAcct1ARN)
			assertContainsNative(t, st, orgTestOUARN, orgTestAcct2ARN)
		})
	}
}

// orgNoIDHandlers is treeHandlers plus elements listed without an Id: an
// OU (with an Arn) under r-ab12, an account and a customer SCP (each with an
// Arn), and a root with neither an Arn nor an Id. The OU listing and
// DescribePolicy record the ids they are sent, and refuse an empty one as the
// API does (InvalidInputException).
func (c *orgCalls) noIDHandlers(sent *[]string) map[string]func(in any) (any, error) {
	h := c.treeHandlers()
	roots, ous, accounts := h["ListRoots"], h["ListOrganizationalUnitsForParent"], h["ListAccounts"]
	record := func(op, id string) error {
		c.mu.Lock()
		*sent = append(*sent, op+" "+id)
		c.mu.Unlock()
		if id == "" {
			return apiErr("InvalidInputException", op+" sent an empty id")
		}
		return nil
	}
	h["ListRoots"] = func(in any) (any, error) {
		out, err := roots(in)
		page := out.(*organizations.ListRootsOutput)
		page.Roots = append(page.Roots, organizationstypes.Root{Name: sdkaws.String("Keyless")})
		return out, err
	}
	h["ListOrganizationalUnitsForParent"] = func(in any) (any, error) {
		parent := sv(in.(*organizations.ListOrganizationalUnitsForParentInput).ParentId)
		if err := record("ListOrganizationalUnitsForParent", parent); err != nil {
			return nil, err
		}
		out, err := ous(in)
		if parent == "r-ab12" {
			page := out.(*organizations.ListOrganizationalUnitsForParentOutput)
			page.OrganizationalUnits = append(page.OrganizationalUnits, organizationstypes.OrganizationalUnit{Arn: sdkaws.String(orgTestNoIDOU), Name: sdkaws.String("NoId")})
		}
		return out, err
	}
	h["ListAccounts"] = func(in any) (any, error) {
		out, err := accounts(in)
		page := out.(*organizations.ListAccountsOutput)
		page.Accounts = append(page.Accounts, organizationstypes.Account{Arn: sdkaws.String(orgTestNoIDAcct), Name: sdkaws.String("five")})
		return out, err
	}
	h["ListPolicies"] = func(any) (any, error) {
		return &organizations.ListPoliciesOutput{Policies: []organizationstypes.PolicySummary{{
			Arn: sdkaws.String(orgTestNoIDSCP), Name: sdkaws.String("NoIdPolicy"), Type: organizationstypes.PolicyTypeServiceControlPolicy,
		}}}, nil
	}
	h["DescribePolicy"] = func(in any) (any, error) {
		if err := record("DescribePolicy", sv(in.(*organizations.DescribePolicyInput).PolicyId)); err != nil {
			return nil, err
		}
		return nil, apiErr("InvalidInputException", "no such fixture")
	}
	return h
}

const (
	orgTestNoIDOU   = "arn:aws:organizations::" + testAccountID + ":ou/o-abc/ou-ab12-44444444"
	orgTestNoIDAcct = "arn:aws:organizations::" + testAccountID + ":account/o-abc/555555555555"
	orgTestNoIDSCP  = "arn:aws:organizations::" + testAccountID + ":policy/o-abc/service_control_policy/p-noid1111"
)

// Elements listed with an Arn but no Id are stored without tags, and an
// OU's children, an account's parent and an SCP's DescribePolicy body are
// not read; no call is sent an empty id. A root with neither an Arn nor an
// Id is not stored and not walked. Each gap is warned about once.
func TestScanOrganizations_ElementsWithoutID(t *testing.T) {
	st := newTestStore(t)
	tagWarns := countOpWarnings(st, "organizations:ListTagsForResource")
	ouWarns := countOpWarnings(st, "organizations:ListOrganizationalUnitsForParent")
	parentWarns := countOpWarnings(st, "organizations:ListParents")
	rootWarns := countOpWarnings(st, "organizations:ListRoots")
	calls := newOrgCalls()
	var sent []string
	total, _, err := orgScanWith(st, calls.noIDHandlers(&sent))
	// org + root + three OUs + five accounts + the SCP.
	if err != nil || total != 11 {
		t.Fatalf("scan = %d, %v; want 11, nil", total, err)
	}
	for typ, arn := range map[string]string{TypeOrganizationsOU: orgTestNoIDOU, TypeOrganizationsAccount: orgTestNoIDAcct, TypeOrganizationsSCP: orgTestNoIDSCP} {
		r, ok := rowsByNativeID(t, st, typ)[arn]
		if !ok || r.TagsJSON != nil {
			t.Errorf("%s stored, TagsJSON = %v, %v; want stored without tags", arn, ok, r.TagsJSON)
		}
	}
	scp := rowsByNativeID(t, st, TypeOrganizationsSCP)[orgTestNoIDSCP]
	var summary organizationstypes.PolicySummary
	if err := json.Unmarshal([]byte(scp.AttributesJSON), &summary); err != nil || sv(summary.Arn) != orgTestNoIDSCP || sv(summary.Name) != "NoIdPolicy" {
		t.Errorf("SCP attrs = %s (%v); want the listed summary", scp.AttributesJSON, err)
	}
	if slices.Contains(calls.sortedTagIDs(), "") {
		t.Error("a tag read was sent an empty ResourceId")
	}
	slices.Sort(sent)
	wantSent := []string{
		"ListOrganizationalUnitsForParent ou-ab12-11111111", "ListOrganizationalUnitsForParent ou-noar-22222222",
		"ListOrganizationalUnitsForParent r-ab12", "ListOrganizationalUnitsForParent r-noarn",
	}
	if !slices.Equal(sent, wantSent) {
		t.Errorf("ids sent = %v\nwant %v", sent, wantSent)
	}
	// ListParents also warns once about the account under the ARN-less root.
	if *tagWarns != 1 || *ouWarns != 1 || *parentWarns != 2 || *rootWarns != 1 {
		t.Errorf("warnings tags, OUs, ListParents, ListRoots = %d, %d, %d, %d; want 1, 1, 2, 1", *tagWarns, *ouWarns, *parentWarns, *rootWarns)
	}
}

// The no-Id warnings name rows stored: after a later phase's failed write,
// which may have lost them, neither is sent.
func TestScanOrganizations_ElementsWithoutIDFailedWriteWarnsNothing(t *testing.T) {
	st, closeStore := openClosingStore(t)
	tagWarns := countOpWarnings(st, "organizations:ListTagsForResource")
	ouWarns := countOpWarnings(st, "organizations:ListOrganizationalUnitsForParent")
	var sent []string
	h := newOrgCalls().noIDHandlers(&sent)
	accounts := h["ListAccounts"]
	h["ListAccounts"] = func(in any) (any, error) {
		closeStore()
		return accounts(in)
	}
	_, _, err := orgScanWith(st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", err)
	}
	if *tagWarns != 0 || *ouWarns != 0 {
		t.Errorf("warnings tags, OUs = %d, %d; want 0, 0 after a failed write", *tagWarns, *ouWarns)
	}
}

// In GovCloud the rows keep the partition's listed ARNs, and the tag reads
// are sent the elements' Ids.
func TestScanOrganizations_GovCloud(t *testing.T) {
	const (
		govOrg  = "arn:aws-us-gov:organizations::" + testAccountID + ":organization/o-gov"
		govRoot = "arn:aws-us-gov:organizations::" + testAccountID + ":root/o-gov/r-ab12"
		govAcct = "arn:aws-us-gov:organizations::" + testAccountID + ":account/o-gov/111111111111"
		govRT   = "arn:aws-us-gov:organizations::" + testAccountID + ":transfer/o-gov/billing/inbound/rt-gov11111"
	)
	st := newTestStore(t)
	calls := newOrgCalls()
	h := map[string]func(in any) (any, error){
		"DescribeOrganization": func(any) (any, error) {
			return &organizations.DescribeOrganizationOutput{Organization: &organizationstypes.Organization{
				Id: sdkaws.String("o-gov"), Arn: sdkaws.String(govOrg), MasterAccountId: sdkaws.String(testAccountID),
			}}, nil
		},
		"ListRoots": func(any) (any, error) {
			return &organizations.ListRootsOutput{Roots: []organizationstypes.Root{{Id: sdkaws.String("r-ab12"), Arn: sdkaws.String(govRoot)}}}, nil
		},
		"ListOrganizationalUnitsForParent": func(any) (any, error) { return &organizations.ListOrganizationalUnitsForParentOutput{}, nil },
		"ListAccounts": func(any) (any, error) {
			return &organizations.ListAccountsOutput{Accounts: []organizationstypes.Account{{Id: sdkaws.String("111111111111"), Arn: sdkaws.String(govAcct)}}}, nil
		},
		"ListParents": func(any) (any, error) {
			return &organizations.ListParentsOutput{Parents: []organizationstypes.Parent{{Id: sdkaws.String("r-ab12")}}}, nil
		},
		"ListPolicies":                        func(any) (any, error) { return &organizations.ListPoliciesOutput{}, nil },
		"DescribeResourcePolicy":              func(any) (any, error) { return nil, apiErr("ResourcePolicyNotFoundException", "none") },
		"ListOutboundResponsibilityTransfers": func(any) (any, error) { return &organizations.ListOutboundResponsibilityTransfersOutput{}, nil },
		"ListInboundResponsibilityTransfers": func(any) (any, error) {
			return &organizations.ListInboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{{Id: sdkaws.String("rt-gov11111"), Arn: sdkaws.String(govRT)}}}, nil
		},
		"ListTagsForResource": calls.listTags,
	}
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, orgTestDenied)
	total, _, err := scanOrganizations(context.Background(), acct, "", st, testScanID)
	if err != nil || total != 4 {
		t.Fatalf("scan = %d, %v; want 4, nil", total, err)
	}
	for typ, want := range map[string]struct{ arn, id string }{
		TypeOrganizationsRoot:                   {govRoot, "r-ab12"},
		TypeOrganizationsAccount:                {govAcct, "111111111111"},
		TypeOrganizationsResponsibilityTransfer: {govRT, "rt-gov11111"},
	} {
		r, ok := rowsByNativeID(t, st, typ)[want.arn]
		if !ok {
			t.Errorf("%s %s not stored", typ, want.arn)
			continue
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"id": want.id})
	}
	if got := calls.sortedTagIDs(); !slices.Equal(got, []string{"111111111111", "r-ab12", "rt-gov11111"}) {
		t.Errorf("tag reads = %v; want the three Ids", got)
	}
	assertContainsNative(t, st, govRoot, govAcct)
}

// ListRoots answering NXDOMAIN after DescribeOrganization reached the host
// is a failure of this scan, not a missing endpoint: the later phases run
// and their rows are counted.
func TestScanOrganizations_LateNXDOMAIN(t *testing.T) {
	st := newTestStore(t)
	h := orgTreeHandlers()
	h["ListRoots"] = func(any) (any, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "organizations.us-east-1.amazonaws.com", IsNotFound: true}
	}
	total, _, err := orgScanWith(st, h)
	assertGlueOutcome(t, err, outcomeError)
	// organization + the four accounts, stored unlinked.
	if total != 5 {
		t.Errorf("total = %d; want 5", total)
	}
}

// A member account stores the organization row alone: no management-only
// listing or tag read is made.
func TestScanOrganizations_MemberAccountStoresOrgOnly(t *testing.T) {
	st := newTestStore(t)
	calls := newOrgCalls()
	h := calls.fullHandlers()
	h["DescribeOrganization"] = func(any) (any, error) {
		return &organizations.DescribeOrganizationOutput{Organization: &organizationstypes.Organization{
			Id: sdkaws.String("o-abc"), Arn: sdkaws.String(orgTestOrgARN), MasterAccountId: sdkaws.String("999999999999"),
		}}, nil
	}
	total, _, err := orgScanWith(st, h)
	if err != nil || total != 1 {
		t.Fatalf("scan = %d, %v; want 1, nil", total, err)
	}
	if len(calls.caps) != 0 || len(calls.sortedTagIDs()) != 0 {
		t.Errorf("listings %v and tag reads %v made; want none", calls.caps, calls.sortedTagIDs())
	}
}

// A root or OU whose write fails is not linked: no closure row names it,
// and the failed write stops the scan, so the accounts are not listed.
func TestScanOrganizations_UnstoredParentIsNotLinked(t *testing.T) {
	for _, tc := range []struct {
		name, refusedType string
		refused           []string
	}{
		{name: "OU write fails", refusedType: TypeOrganizationsOU, refused: []string{orgTestOUARN, orgTestNoARNOU}},
		{name: "root write fails", refusedType: TypeOrganizationsRoot, refused: []string{orgTestRootARN}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			// The abort is a constraint error, which the store does not retry.
			if _, err := st.DB().Exec(`CREATE TRIGGER refuse_parent BEFORE INSERT ON resources
				WHEN NEW.type = '` + tc.refusedType + `'
				BEGIN SELECT RAISE(ABORT, 'test: parent write refused'); END`); err != nil {
				t.Fatalf("create trigger: %v", err)
			}
			_, _, err := orgScanWith(st, orgTreeHandlers())
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Errorf("err = %v; want the parent write failure", err)
			}
			for _, arn := range tc.refused {
				id := store.ResourceID("aws", testAccountID, arn)
				var n int
				if err := st.DB().Get(&n, `SELECT COUNT(*) FROM hierarchy_closure WHERE ancestor_id = ? OR descendant_id = ?`, id, id); err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Errorf("closure rows naming unstored %s = %d; want 0", arn, n)
				}
			}
			if n := len(rowsByNativeID(t, st, TypeOrganizationsAccount)); n != 0 {
				t.Errorf("account rows = %d; want 0: the failed write stops the scan", n)
			}
		})
	}
}

// Denied OU listings on two levels of the walk (r-noarn, then
// ou-ab12-11111111) are one warning, and the OUs listed elsewhere are stored.
func TestScanOrganizations_OUWalkDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "organizations:ListOrganizationalUnitsForParent")
	h := newOrgCalls().fullHandlers()
	ous := h["ListOrganizationalUnitsForParent"]
	h["ListOrganizationalUnitsForParent"] = func(in any) (any, error) {
		switch sv(in.(*organizations.ListOrganizationalUnitsForParentInput).ParentId) {
		case "r-noarn", "ou-ab12-11111111":
			return nil, orgTestDenied
		}
		return ous(in)
	}
	if _, _, err := orgScanWith(st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("OU listing warnings = %d; want 1", *warns)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeOrganizationsOU)); !slices.Equal(got, []string{orgTestOUARN}) {
		t.Errorf("OU rows = %v; want %s", got, orgTestOUARN)
	}
}

// A denied OU listing (r-noarn) is not warned about when a later level's
// write (ou-ab12-11111111's children) fails: the warning would follow rows a
// failed write may have lost.
func TestScanOrganizations_OUWalkDenialFailedWriteWarnsNothing(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, "organizations:ListOrganizationalUnitsForParent")
	h := newOrgCalls().fullHandlers()
	ous := h["ListOrganizationalUnitsForParent"]
	h["ListOrganizationalUnitsForParent"] = func(in any) (any, error) {
		switch sv(in.(*organizations.ListOrganizationalUnitsForParentInput).ParentId) {
		case "r-noarn":
			return nil, orgTestDenied
		case "ou-ab12-11111111":
			closeStore()
		}
		return ous(in)
	}
	_, _, err := orgScanWith(st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", err)
	}
	if *warns != 0 {
		t.Errorf("OU listing warnings = %d; want 0 after a failed write", *warns)
	}
}

// A resource policy described without an Arn is not stored, its tags are
// not read, and it is warned about once under DescribeResourcePolicy.
func TestScanOrganizations_ARNlessResourcePolicyNotStored(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "organizations:DescribeResourcePolicy")
	calls := newOrgCalls()
	h := calls.fullHandlers()
	h["DescribeResourcePolicy"] = func(any) (any, error) {
		return &organizations.DescribeResourcePolicyOutput{ResourcePolicy: &organizationstypes.ResourcePolicy{
			ResourcePolicySummary: &organizationstypes.ResourcePolicySummary{Id: sdkaws.String("rp-abcd1234")},
			Content:               sdkaws.String(`{"Version":"2012-10-17"}`),
		}}, nil
	}
	if _, _, err := orgScanWith(st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rows := rowsByNativeID(t, st, TypeOrganizationsResourcePolicy); len(rows) != 0 {
		t.Errorf("resource policy rows = %v; want none", nativeIDs(rows))
	}
	if *warns != 1 {
		t.Errorf("organizations:DescribeResourcePolicy warnings = %d; want 1", *warns)
	}
	if slices.Contains(calls.sortedTagIDs(), "rp-abcd1234") {
		t.Error("tags read for the resource policy that was not stored")
	}
}

// ctx ending while DescribeOrganization answers stops the scan before any
// listing, and the organization row it stored is counted.
func TestScanOrganizations_StopAfterOrganizationCountsIt(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := newOrgCalls()
	h := calls.treeHandlers()
	describe := h["DescribeOrganization"]
	h["DescribeOrganization"] = func(in any) (any, error) {
		cancel()
		return describe(in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, orgTestDenied)
	total, _, err := scanOrganizations(ctx, acct, "", st, testScanID)
	if !errors.Is(err, context.Canceled) || total != 1 {
		t.Fatalf("scan = %d, %v; want 1, context.Canceled", total, err)
	}
	if len(rowsByNativeID(t, st, TypeOrganization)) != 1 || len(calls.caps) != 0 {
		t.Errorf("organization rows = %d, listings = %v; want 1 row, no listing", len(rowsByNativeID(t, st, TypeOrganization)), calls.caps)
	}
}

// Whichever list returns it, a transfer whose ARN names another account is
// kept untagged and silent when its tag read is denied or finds nothing.
func TestScanOrganizations_OtherAccountOutboundTransferKeptUntagged(t *testing.T) {
	const otherOut = "arn:aws:organizations::888888888888:transfer/o-other2/billing/outbound/rt-out11111"
	for name, tagErr := range map[string]error{"denied": orgTestDenied, "not found": apiErr("TargetNotFoundException", "gone")} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "organizations:ListTagsForResource")
			calls := newOrgCalls()
			calls.tagErrs["rt-out11111"] = tagErr
			h := calls.fullHandlers()
			h["ListOutboundResponsibilityTransfers"] = func(any) (any, error) {
				return &organizations.ListOutboundResponsibilityTransfersOutput{ResponsibilityTransfers: []organizationstypes.ResponsibilityTransfer{{
					Id: sdkaws.String("rt-out11111"), Arn: sdkaws.String(otherOut), Name: sdkaws.String("outbound"),
				}}}, nil
			}
			total, _, err := orgScanWith(st, h)
			if err != nil || total != orgTestFullTotal {
				t.Fatalf("scan = %d, %v; want %d, nil", total, err, orgTestFullTotal)
			}
			r, ok := rowsByNativeID(t, st, TypeOrganizationsResponsibilityTransfer)[otherOut]
			if !ok || r.TagsJSON != nil {
				t.Errorf("transfer stored, TagsJSON = %v, %v; want stored without tags", ok, r.TagsJSON)
			}
			if *warns != 0 {
				t.Errorf("tag warnings = %d; want 0", *warns)
			}
		})
	}
}
