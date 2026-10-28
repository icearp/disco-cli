package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture: ListClusters returns c1 on its first page and c2 on its
// second. Every per-cluster listing returns, for each cluster, its "t" item on
// the first page and its "b" item on the second. Every resource whose name
// ends in "-t" has the tags {"type": <its type>}; the "b" ones have none.
// The child listings of the associated access policies and certificate
// authorities, and the EKS Anywhere subscriptions, are empty.

var (
	eksxThrottled = apiErr("ThrottlingException", "rate exceeded")
	eksxTagGone   = apiErr("NotFoundException", "no such resource")
)

// eksxCalls records the stubbed calls and answers every tag read from tags
// and errs, both keyed by the row's NativeID. tagARNs records each tag read
// as "<op> <what it was sent>".
type eksxCalls struct {
	*bacCalls
	partition, region string
}

func newEKSXCalls() *eksxCalls { return newEKSXCallsIn("aws", testRegion) }

func newEKSXCallsIn(partition, region string) *eksxCalls {
	c := &eksxCalls{bacCalls: newBACCalls(), partition: partition, region: region}
	for _, w := range c.rows() {
		if strings.HasSuffix(w.name, "-t") {
			c.tags[w.nativeID] = map[string]string{"type": w.typ}
		}
	}
	return c
}

func (c *eksxCalls) arn(res string) string {
	return arnFor(c.partition, "eks", c.region, testAccountID, res)
}

// childID is the NativeID of a resource listed by name alone.
func (c *eksxCalls) childID(kind, cluster, key string) string {
	return c.arn(kind + "/" + cluster + "/" + key)
}

// principal holds "/" and ":" after the account, as an IAM path may.
func (c *eksxCalls) principal(cluster, n string) string {
	return arnFor(c.partition, "iam", "", testAccountID, "role/team:"+cluster+"/p-"+n)
}

func (c *eksxCalls) capability(cluster, n string) ekstypes.CapabilitySummary {
	name := "cap-" + cluster + "-" + n
	return ekstypes.CapabilitySummary{
		Arn: sdkaws.String(c.arn("capability/" + cluster + "/argocd/" + name + "/u-1")), CapabilityName: &name,
		Type: ekstypes.CapabilityTypeArgocd, Status: ekstypes.CapabilityStatusActive, CreatedAt: &eksCACreated,
	}
}

func (c *eksxCalls) podIdentity(cluster, n string) ekstypes.PodIdentityAssociationSummary {
	id := "a-" + cluster + "-" + n
	return ekstypes.PodIdentityAssociationSummary{
		AssociationArn: sdkaws.String(c.arn("podidentityassociation/" + cluster + "/" + id)), AssociationId: &id,
		ClusterName: &cluster, Namespace: sdkaws.String("ns"), ServiceAccount: sdkaws.String("sa"),
	}
}

func eksxIDP(cluster, n string) ekstypes.IdentityProviderConfig {
	return ekstypes.IdentityProviderConfig{Name: sdkaws.String("idp-" + cluster + "-" + n), Type: sdkaws.String("oidc")}
}

// eksxWant is a row the fixture stores: its Name, the attributes it keeps
// from the base scan, and its tag read as tagARNs records it.
type eksxWant struct{ typ, nativeID, name, attrs, read string }

// rows is every per-cluster row the fixture stores.
func (c *eksxCalls) rows() []eksxWant {
	var rows []eksxWant
	for _, cl := range []string{"c1", "c2"} {
		for _, n := range []string{"t", "b"} {
			named := func(typ, kind, nameKey, describeOp, name string) eksxWant {
				return eksxWant{
					typ, c.childID(kind, cl, name), name, mustJSON(map[string]string{"ClusterName": cl, nameKey: name}),
					describeOp + " " + cl + " " + name,
				}
			}
			listed := func(typ, arn, name string, element any) eksxWant {
				return eksxWant{typ, arn, name, mustJSON(element), "ListTagsForResource " + arn}
			}
			cp, pia, idp := c.capability(cl, n), c.podIdentity(cl, n), eksxIDP(cl, n)
			idpKey := "oidc/" + sv(idp.Name)
			rows = append(rows,
				named(TypeEKSAccessEntry, "access-entry", "PrincipalArn", "DescribeAccessEntry", c.principal(cl, n)),
				named(TypeEKSAddon, "addon", "AddonName", "DescribeAddon", "addon-"+cl+"-"+n),
				listed(TypeEKSCapability, sv(cp.Arn), sv(cp.CapabilityName), cp),
				named(TypeEKSFargateProfile, "fargateprofile", "FargateProfileName", "DescribeFargateProfile", "fp-"+cl+"-"+n),
				eksxWant{
					TypeEKSIdentityProviderConfig, c.childID("identityproviderconfig", cl, idpKey), sv(idp.Name), mustJSON(idp),
					"DescribeIdentityProviderConfig " + cl + " " + idpKey,
				},
				named(TypeEKSNodegroup, "nodegroup", "NodegroupName", "DescribeNodegroup", "ng-"+cl+"-"+n),
				eksxWant{
					TypeEKSPodIdentityAssociation, sv(pia.AssociationArn), sv(pia.AssociationId), mustJSON(pia),
					"DescribePodIdentityAssociation " + cl + " " + sv(pia.AssociationId),
				},
			)
		}
	}
	return rows
}

func (c *eksxCalls) tagRead(read, nativeID string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, read)
	return c.tags[nativeID], c.errs[nativeID]
}

func (c *eksxCalls) listTags(in any) (any, error) {
	arn := sv(in.(*eks.ListTagsForResourceInput).ResourceArn)
	tags, err := c.tagRead("ListTagsForResource "+arn, arn)
	if err != nil {
		return nil, err
	}
	return &eks.ListTagsForResourceOutput{Tags: tags}, nil
}

// describe answers op, a Describe of a resource of kind listed by name alone:
// key is what the call names it by, out the answer carrying tags.
func (c *eksxCalls) describe(op, kind string, key func(in any) string, out func(tags map[string]string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		cluster, k := sv(bacField[*string](in, "ClusterName")), key(in)
		tags, err := c.tagRead(op+" "+cluster+" "+k, c.childID(kind, cluster, k))
		if err != nil {
			return nil, err
		}
		return out(tags), nil
	}
}

func (c *eksxCalls) cluster(name string) *ekstypes.Cluster {
	return &ekstypes.Cluster{
		Arn: sdkaws.String(c.arn("cluster/" + name)), Name: &name, Status: ekstypes.ClusterStatusActive,
		CreatedAt: &eksCACreated, Tags: map[string]string{"cluster": name},
	}
}

// eksxListOps is every per-cluster listing, in phase order.
var eksxListOps = []string{
	"ListAccessEntries", "ListAddons", "ListCapabilities", "ListFargateProfiles",
	"ListIdentityProviderConfigs", "ListNodegroups", "ListPodIdentityAssociations",
}

// handlers answers every op the scan calls with the fixture.
func (c *eksxCalls) handlers() map[string]func(any) (any, error) {
	pages := func(op string, out func(cluster, n string, next *string) any) func(any) (any, error) {
		return c.answer(op, func(in any) any {
			cluster := sv(bacField[*string](in, "ClusterName"))
			if bacField[*string](in, "NextToken") == nil {
				return out(cluster, "t", sdkaws.String("p2"))
			}
			return out(cluster, "b", nil)
		})
	}
	return map[string]func(any) (any, error){
		"ListClusters": c.answer("ListClusters", func(in any) any {
			if in.(*eks.ListClustersInput).NextToken == nil {
				return &eks.ListClustersOutput{Clusters: []string{"c1"}, NextToken: sdkaws.String("p2")}
			}
			return &eks.ListClustersOutput{Clusters: []string{"c2"}}
		}),
		"DescribeCluster": c.answer("DescribeCluster", func(in any) any {
			return &eks.DescribeClusterOutput{Cluster: c.cluster(sv(in.(*eks.DescribeClusterInput).Name))}
		}),
		"ListAccessEntries": pages("ListAccessEntries", func(cl, n string, next *string) any {
			return &eks.ListAccessEntriesOutput{AccessEntries: []string{c.principal(cl, n)}, NextToken: next}
		}),
		"ListAddons": pages("ListAddons", func(cl, n string, next *string) any {
			return &eks.ListAddonsOutput{Addons: []string{"addon-" + cl + "-" + n}, NextToken: next}
		}),
		"ListCapabilities": pages("ListCapabilities", func(cl, n string, next *string) any {
			return &eks.ListCapabilitiesOutput{Capabilities: []ekstypes.CapabilitySummary{c.capability(cl, n)}, NextToken: next}
		}),
		"ListFargateProfiles": pages("ListFargateProfiles", func(cl, n string, next *string) any {
			return &eks.ListFargateProfilesOutput{FargateProfileNames: []string{"fp-" + cl + "-" + n}, NextToken: next}
		}),
		"ListIdentityProviderConfigs": pages("ListIdentityProviderConfigs", func(cl, n string, next *string) any {
			return &eks.ListIdentityProviderConfigsOutput{IdentityProviderConfigs: []ekstypes.IdentityProviderConfig{eksxIDP(cl, n)}, NextToken: next}
		}),
		"ListNodegroups": pages("ListNodegroups", func(cl, n string, next *string) any {
			return &eks.ListNodegroupsOutput{Nodegroups: []string{"ng-" + cl + "-" + n}, NextToken: next}
		}),
		"ListPodIdentityAssociations": pages("ListPodIdentityAssociations", func(cl, n string, next *string) any {
			return &eks.ListPodIdentityAssociationsOutput{Associations: []ekstypes.PodIdentityAssociationSummary{c.podIdentity(cl, n)}, NextToken: next}
		}),
		"ListTagsForResource": c.listTags,
		"DescribeAccessEntry": c.describe("DescribeAccessEntry", "access-entry",
			func(in any) string { return sv(in.(*eks.DescribeAccessEntryInput).PrincipalArn) },
			func(tags map[string]string) any {
				return &eks.DescribeAccessEntryOutput{AccessEntry: &ekstypes.AccessEntry{Tags: tags}}
			}),
		"DescribeAddon": c.describe("DescribeAddon", "addon",
			func(in any) string { return sv(in.(*eks.DescribeAddonInput).AddonName) },
			func(tags map[string]string) any { return &eks.DescribeAddonOutput{Addon: &ekstypes.Addon{Tags: tags}} }),
		"DescribeFargateProfile": c.describe("DescribeFargateProfile", "fargateprofile",
			func(in any) string { return sv(in.(*eks.DescribeFargateProfileInput).FargateProfileName) },
			func(tags map[string]string) any {
				return &eks.DescribeFargateProfileOutput{FargateProfile: &ekstypes.FargateProfile{Tags: tags}}
			}),
		"DescribeIdentityProviderConfig": c.describe("DescribeIdentityProviderConfig", "identityproviderconfig",
			func(in any) string {
				cfg := in.(*eks.DescribeIdentityProviderConfigInput).IdentityProviderConfig
				return sv(cfg.Type) + "/" + sv(cfg.Name)
			},
			func(tags map[string]string) any {
				return &eks.DescribeIdentityProviderConfigOutput{IdentityProviderConfig: &ekstypes.IdentityProviderConfigResponse{Oidc: &ekstypes.OidcIdentityProviderConfig{Tags: tags}}}
			}),
		"DescribeNodegroup": c.describe("DescribeNodegroup", "nodegroup",
			func(in any) string { return sv(in.(*eks.DescribeNodegroupInput).NodegroupName) },
			func(tags map[string]string) any {
				return &eks.DescribeNodegroupOutput{Nodegroup: &ekstypes.Nodegroup{Tags: tags}}
			}),
		"DescribePodIdentityAssociation": c.describe("DescribePodIdentityAssociation", "podidentityassociation",
			func(in any) string { return sv(in.(*eks.DescribePodIdentityAssociationInput).AssociationId) },
			func(tags map[string]string) any {
				return &eks.DescribePodIdentityAssociationOutput{Association: &ekstypes.PodIdentityAssociation{Tags: tags}}
			}),
		"ListEksAnywhereSubscriptions": c.answer("ListEksAnywhereSubscriptions", func(any) any { return &eks.ListEksAnywhereSubscriptionsOutput{} }),
		"ListAssociatedAccessPolicies": c.answer("ListAssociatedAccessPolicies", func(any) any { return &eks.ListAssociatedAccessPoliciesOutput{} }),
		"ListCertificateAuthorities":   c.answer("ListCertificateAuthorities", func(any) any { return &eks.ListCertificateAuthoritiesOutput{} }),
	}
}

// eksxScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func eksxScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, eksDenied)
	return scanEKS(ctx, acct, testRegion, st, testScanID)
}

func eksxRowOf(t *testing.T, st *store.Store, typ, nativeID string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[nativeID]
	return r, ok
}

// eksxAssertRows checks that every per-cluster row of the fixture c answers
// is stored with the tags its read returned (nil for none) and the base
// scan's Name, Region and attributes, no Status and no CreatedAt; and that
// exactly those rows' identifiers were sent to the tag reads.
func eksxAssertRows(t *testing.T, st *store.Store, c *eksxCalls) {
	t.Helper()
	for _, w := range c.rows() {
		r, ok := eksxRowOf(t, st, w.typ, w.nativeID)
		if !ok {
			t.Errorf("%s %s not stored", w.typ, w.nativeID)
			continue
		}
		if strings.HasSuffix(w.name, "-t") {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", w.typ, w.nativeID, *r.TagsJSON)
		}
		if sv(r.Name) != w.name || sv(r.Region) != c.region || r.AttributesJSON != w.attrs {
			t.Errorf("%s %s Name, Region, attributes = %v, %v, %s; want %s, %s, %s", w.typ, w.nativeID, r.Name, r.Region, r.AttributesJSON, w.name, c.region, w.attrs)
		}
		if r.Status != nil || r.CreatedAt != nil {
			t.Errorf("%s %s Status, CreatedAt = %v, %v; want unset", w.typ, w.nativeID, r.Status, r.CreatedAt)
		}
	}
	var want []string
	for _, w := range c.rows() {
		want = append(want, w.read)
	}
	slices.Sort(want)
	got := slices.Clone(c.tagARNs)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("tag reads = %v; want %v", got, want)
	}
}

// Every per-cluster type stores the tags its read returns, nil for a
// resource without tags; the capabilities are read with ListTagsForResource
// on their listed ARN, the pod identity associations with
// DescribePodIdentityAssociation on the cluster listed and the element's
// AssociationId, the others with their Describe on the cluster and name their
// row was built from. Both pages of
// every listing and both clusters are read, every listing asks for the
// model's page cap of 100, and the scan reports no error or warning.
func TestScanEKS_StoresTagsOfEveryPerClusterType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newEKSXCalls()

	total, inserted, err := eksxScan(context.Background(), st, calls.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 30 || inserted != 30 {
		t.Errorf("total, inserted = %d, %d; want 30 (2 clusters, 28 per-cluster rows)", total, inserted)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	eksxAssertRows(t, st, calls)
	for _, op := range append([]string{"ListClusters"}, eksxListOps...) {
		want := 4 // two pages for each of two clusters
		if op == "ListClusters" {
			want = 2
		}
		caps := calls.maxResults[op]
		if len(caps) != want {
			t.Errorf("%s calls with MaxResults = %d; want %d", op, len(caps), want)
		}
		for _, n := range caps {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
}

// The NativeIDs of the types listed by name alone keep their base shape.
func TestScanEKS_NamedChildNativeIDs(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := eksxScan(context.Background(), st, newEKSXCalls().handlers()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for typ, id := range map[string]string{
		TypeEKSAccessEntry:            "arn:aws:eks:us-east-1:123456789012:access-entry/c1/arn:aws:iam::123456789012:role/team:c1/p-t",
		TypeEKSAddon:                  "arn:aws:eks:us-east-1:123456789012:addon/c1/addon-c1-t",
		TypeEKSFargateProfile:         "arn:aws:eks:us-east-1:123456789012:fargateprofile/c1/fp-c1-t",
		TypeEKSIdentityProviderConfig: "arn:aws:eks:us-east-1:123456789012:identityproviderconfig/c1/oidc/idp-c1-t",
		TypeEKSNodegroup:              "arn:aws:eks:us-east-1:123456789012:nodegroup/c1/ng-c1-t",
	} {
		if _, ok := eksxRowOf(t, st, typ, id); !ok {
			t.Errorf("%s %s not stored", typ, id)
		}
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends its gov ARNs to
// ListTagsForResource unchanged, and the cluster and key of its gov NativeIDs
// to the Describes.
func TestScanEKS_GovCloudRowsReachTagReads(t *testing.T) {
	const govRegion = "us-gov-west-1"
	st := newTestStore(t)
	calls := newEKSXCallsIn("aws-us-gov", govRegion)
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(calls.handlers(), eksDenied)

	total, _, err := scanEKS(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 30 {
		t.Fatalf("scan = %d, %v; want 30, nil", total, err)
	}
	eksxAssertRows(t, st, calls)
	if !slices.Contains(calls.tagARNs, "ListTagsForResource arn:aws-us-gov:eks:us-gov-west-1:123456789012:capability/c1/argocd/cap-c1-t/u-1") {
		t.Errorf("tag reads = %v; want the gov capability ARN among them", calls.tagARNs)
	}
}

// Per-cluster listings returning nothing store nothing, read no tags and
// report nothing.
func TestScanEKS_EmptyPerClusterListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newEKSXCalls()
	h := calls.handlers()
	// Constructors, not outputs: the SDK writes ResultMetadata into each
	// output it returns, and the clusters are listed concurrently.
	empty := map[string]func() any{
		"ListAccessEntries":           func() any { return &eks.ListAccessEntriesOutput{} },
		"ListAddons":                  func() any { return &eks.ListAddonsOutput{} },
		"ListCapabilities":            func() any { return &eks.ListCapabilitiesOutput{} },
		"ListFargateProfiles":         func() any { return &eks.ListFargateProfilesOutput{} },
		"ListIdentityProviderConfigs": func() any { return &eks.ListIdentityProviderConfigsOutput{} },
		"ListNodegroups":              func() any { return &eks.ListNodegroupsOutput{} },
		"ListPodIdentityAssociations": func() any { return &eks.ListPodIdentityAssociationsOutput{} },
	}
	for op, out := range empty {
		h[op] = calls.answer(op, func(any) any { return out() })
	}

	total, _, err := eksxScan(context.Background(), st, h)
	if err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2 (the clusters), nil", total, err)
	}
	if *warns != 0 || len(calls.tagARNs) != 0 || calls.count("ListAssociatedAccessPolicies") != 0 {
		t.Errorf("warnings, tag reads, ListAssociatedAccessPolicies calls = %d, %v, %d; want 0, none, 0",
			*warns, calls.tagARNs, calls.count("ListAssociatedAccessPolicies"))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanEKS_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := eksxScan(context.Background(), st, newEKSXCalls().handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := eksxScan(context.Background(), st, newEKSXCalls().handlers())
	if err != nil || total != 30 || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want 30, 0, nil", total, inserted, err)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under the tag op, a throttle after retries is a
// transient error, any other code a hard one; the tag op's modeled
// not-found drops the row. ResourceNotFoundException, which
// ListTagsForResource does not model, is a hard error there.
func TestScanEKS_TagReadFailures(t *testing.T) {
	calls := newEKSXCalls()
	for _, target := range []struct {
		typ, nativeID, tagOp string
		notFound             error
	}{
		{TypeEKSCapability, sv(calls.capability("c1", "t").Arn), "eks:ListTagsForResource", eksxTagGone},
		{TypeEKSAddon, calls.childID("addon", "c1", "addon-c1-t"), "eks:DescribeAddon", eksGone},
		{TypeEKSIdentityProviderConfig, calls.childID("identityproviderconfig", "c1", "oidc/idp-c1-t"), "eks:DescribeIdentityProviderConfig", eksGone},
		{TypeEKSPodIdentityAssociation, sv(calls.podIdentity("c1", "t").AssociationArn), "eks:DescribePodIdentityAssociation", eksGone},
	} {
		t.Run(target.typ, func(t *testing.T) {
			cases := []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", eksDenied, true, 0, 1},
				{"not found", target.notFound, false, 0, 0},
				{"other error", eksBadInput, true, outcomeError, 0},
				{"transient", eksxThrottled, true, outcomeTransient, 0},
			}
			if target.tagOp == "eks:ListTagsForResource" {
				cases = append(cases, struct {
					name      string
					err       error
					kept      bool
					outcome   serviceOutcome
					wantWarns int
				}{"unmodeled not found", eksGone, true, outcomeError, 0})
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, target.tagOp)
					calls := newEKSXCalls()
					calls.errs[target.nativeID] = c.err

					total, _, err := eksxScan(context.Background(), st, calls.handlers())
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
						if !errors.Is(err, c.err) {
							t.Errorf("err = %v; want it to carry %v", err, c.err)
						}
					}
					r, ok := eksxRowOf(t, st, target.typ, target.nativeID)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := 30
					if !c.kept {
						want = 29
					}
					if total != want {
						t.Errorf("total = %d; want %d", total, want)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", target.tagOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads of both clusters' rows are one warning under the tag op
// for the scan.
func TestScanEKS_TagDenialsWarnOnce(t *testing.T) {
	calls := newEKSXCalls()
	for _, c := range []struct{ typ, tagOp, c1, c2 string }{
		{TypeEKSCapability, "eks:ListTagsForResource", sv(calls.capability("c1", "t").Arn), sv(calls.capability("c2", "t").Arn)},
		{TypeEKSPodIdentityAssociation, "eks:DescribePodIdentityAssociation", sv(calls.podIdentity("c1", "t").AssociationArn), sv(calls.podIdentity("c2", "t").AssociationArn)},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, c.tagOp)
			calls := newEKSXCalls()
			calls.errs[c.c1] = eksDenied
			calls.errs[c.c2] = eksDenied

			if _, _, err := eksxScan(context.Background(), st, calls.handlers()); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("%s warnings = %v; want one, scoped %s/%s", c.tagOp, *warnings, testAccountID, testRegion)
			}
			for _, id := range []string{c.c1, c.c2} {
				if r, ok := eksxRowOf(t, st, c.typ, id); !ok || r.TagsJSON != nil {
					t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", id, ok, r.TagsJSON)
				}
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanEKS_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "eks:DescribeNodegroup")
	calls := newEKSXCalls()
	calls.errs[calls.childID("nodegroup", "c1", "ng-c1-t")] = eksxThrottled
	calls.errs[calls.childID("nodegroup", "c2", "ng-c2-b")] = eksBadInput

	_, _, err := eksxScan(context.Background(), st, calls.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterException")
	if *warns != 1 {
		t.Errorf("eks:DescribeNodegroup warnings = %d; want 1", *warns)
	}
}

// A Describe answering without the resource keeps the row untagged and fails
// the scan with errEKSNoBody, rather than reading the row as untagged.
func TestScanEKS_DescribeWithoutBodyIsAnError(t *testing.T) {
	calls := newEKSXCalls()
	for _, c := range []struct{ op, typ, nativeID string }{
		{"DescribeAccessEntry", TypeEKSAccessEntry, calls.childID("access-entry", "c1", calls.principal("c1", "t"))},
		{"DescribeAddon", TypeEKSAddon, calls.childID("addon", "c1", "addon-c1-t")},
		{"DescribeFargateProfile", TypeEKSFargateProfile, calls.childID("fargateprofile", "c1", "fp-c1-t")},
		{"DescribeIdentityProviderConfig", TypeEKSIdentityProviderConfig, calls.childID("identityproviderconfig", "c1", "oidc/idp-c1-t")},
		{"DescribeNodegroup", TypeEKSNodegroup, calls.childID("nodegroup", "c1", "ng-c1-t")},
		{"DescribePodIdentityAssociation", TypeEKSPodIdentityAssociation, sv(calls.podIdentity("c1", "t").AssociationArn)},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newEKSXCalls()
			h := calls.handlers()
			// Constructors, not outputs: the SDK writes ResultMetadata into
			// each output it returns, and the rows are read concurrently.
			outputs := map[string]func() any{
				"DescribeAccessEntry":    func() any { return &eks.DescribeAccessEntryOutput{} },
				"DescribeAddon":          func() any { return &eks.DescribeAddonOutput{} },
				"DescribeFargateProfile": func() any { return &eks.DescribeFargateProfileOutput{} },
				"DescribeIdentityProviderConfig": func() any {
					return &eks.DescribeIdentityProviderConfigOutput{IdentityProviderConfig: &ekstypes.IdentityProviderConfigResponse{}}
				},
				"DescribeNodegroup":              func() any { return &eks.DescribeNodegroupOutput{} },
				"DescribePodIdentityAssociation": func() any { return &eks.DescribePodIdentityAssociationOutput{} },
			}
			h[c.op] = func(any) (any, error) { return outputs[c.op](), nil }

			total, _, err := eksxScan(context.Background(), st, h)
			if !errors.Is(err, errEKSNoBody) {
				t.Fatalf("err = %v; want errEKSNoBody", err)
			}
			assertGlueOutcome(t, err, outcomeError)
			if r, ok := eksxRowOf(t, st, c.typ, c.nativeID); !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", c.nativeID, ok, r.TagsJSON)
			}
			if total != 30 {
				t.Errorf("total = %d; want 30", total)
			}
		})
	}
}

// A denied per-cluster listing warns once under its op and costs no other
// phase its rows.
func TestScanEKS_DeniedPerClusterListingWarnsUnderItsOp(t *testing.T) {
	for _, op := range eksxListOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "eks:"+op)
			calls := newEKSXCalls()
			h := calls.handlers()
			h[op] = func(any) (any, error) { return nil, eksDenied }

			total, _, err := eksxScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (eks:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			if stored := countStoredRows(t, st); total != 26 || stored != 26 {
				t.Errorf("total = %d, stored rows = %d; want 26", total, stored)
			}
		})
	}
}

// A cluster deleted since ListClusters returned it fails its listing with
// ResourceNotFoundException: the listings that model it skip that cluster
// silently and store the other's rows; ListCapabilities, which does not,
// returns it.
func TestScanEKS_ClusterGoneFromPerClusterListing(t *testing.T) {
	for _, op := range eksxListOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newEKSXCalls()
			h := calls.handlers()
			list := h[op]
			h[op] = func(in any) (any, error) {
				if sv(bacField[*string](in, "ClusterName")) == "c1" {
					return nil, eksGone
				}
				return list(in)
			}

			total, _, err := eksxScan(context.Background(), st, h)
			if op == "ListCapabilities" {
				assertAPICode(t, err, "ResourceNotFoundException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 0 || total != 28 {
				t.Errorf("warnings, total = %d, %d; want 0, 28", *warns, total)
			}
		})
	}
}

// A per-cluster listing's failed page keeps the page read before it and the
// other cluster's rows, and its error does not stop the later phases.
func TestScanEKS_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newEKSXCalls()
	h := calls.handlers()
	list := h["ListNodegroups"]
	h["ListNodegroups"] = func(in any) (any, error) {
		if sv(bacField[*string](in, "ClusterName")) == "c1" && bacField[*string](in, "NextToken") != nil {
			return nil, eksBadInput
		}
		return list(in)
	}

	total, _, err := eksxScan(context.Background(), st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if total != 29 {
		t.Errorf("total = %d; want 29", total)
	}
	if _, ok := eksxRowOf(t, st, TypeEKSNodegroup, calls.childID("nodegroup", "c1", "ng-c1-t")); !ok {
		t.Error("ng-c1-t, from the page read, missing")
	}
	if _, ok := eksxRowOf(t, st, TypeEKSPodIdentityAssociation, sv(calls.podIdentity("c2", "b").AssociationArn)); !ok {
		t.Error("a later phase's row missing")
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanEKS_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newEKSXCalls()
	h := calls.handlers()
	h["ListAddons"] = func(in any) (any, error) {
		cl := sv(bacField[*string](in, "ClusterName"))
		return &eks.ListAddonsOutput{Addons: []string{"addon-" + cl + "-t"}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := eksxScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := eksxRowOf(t, st, TypeEKSAddon, calls.childID("addon", "c1", "addon-c1-t")); !ok {
		t.Error("addon-c1-t, from the pages read, missing")
	}
}

// A capability listed without a CapabilityName, and a pod identity
// association without an AssociationId, are stored named by their ARN. The
// capability's ARN reaches ListTagsForResource; the association's Describe is
// sent its cluster and the empty AssociationId it was listed with (the SDK
// refuses to send such a call, so outside this stub the read fails).
func TestScanEKS_UnnamedListedRowsNamedByARN(t *testing.T) {
	st := newTestStore(t)
	calls := newEKSXCalls()
	h := calls.handlers()
	capARN := func(cl string) string { return calls.arn("capability/" + cl + "/argocd/unnamed/u-9") }
	piaARN := func(cl string) string { return calls.arn("podidentityassociation/" + cl + "/a-unnamed") }
	h["ListCapabilities"] = calls.answer("ListCapabilities", func(in any) any {
		arn := capARN(sv(bacField[*string](in, "ClusterName")))
		return &eks.ListCapabilitiesOutput{Capabilities: []ekstypes.CapabilitySummary{{Arn: &arn}}}
	})
	h["ListPodIdentityAssociations"] = calls.answer("ListPodIdentityAssociations", func(in any) any {
		arn := piaARN(sv(bacField[*string](in, "ClusterName")))
		return &eks.ListPodIdentityAssociationsOutput{Associations: []ekstypes.PodIdentityAssociationSummary{{AssociationArn: &arn}}}
	})

	total, _, err := eksxScan(context.Background(), st, h)
	if err != nil || total != 26 {
		t.Fatalf("scan = %d, %v; want 26, nil", total, err)
	}
	for _, cl := range []string{"c1", "c2"} {
		for typ, arn := range map[string]string{TypeEKSCapability: capARN(cl), TypeEKSPodIdentityAssociation: piaARN(cl)} {
			if r, ok := eksxRowOf(t, st, typ, arn); !ok || sv(r.Name) != arn {
				t.Errorf("%s %s stored %v, Name %v; want it stored named by its ARN", typ, arn, ok, r.Name)
			}
		}
		for _, read := range []string{"ListTagsForResource " + capARN(cl), "DescribePodIdentityAssociation " + cl + " "} {
			if !slices.Contains(calls.tagARNs, read) {
				t.Errorf("tag reads = %v; want %q among them", calls.tagARNs, read)
			}
		}
	}
}

// Items listed without the name or ARN their row needs are not stored or
// read and are warned about once per listing, under a key that is never
// empty.
func TestScanEKS_UnstorableItemsWarn(t *testing.T) {
	// out builds a fresh output per call: the SDK writes ResultMetadata into
	// each output it returns, and the clusters are listed concurrently.
	for _, c := range []struct {
		op, key string
		out     func() any
	}{
		{"ListAccessEntries", "first (no name) in cluster c", func() any { return &eks.ListAccessEntriesOutput{AccessEntries: []string{""}} }},
		{"ListAddons", "first (no name) in cluster c", func() any { return &eks.ListAddonsOutput{Addons: []string{""}} }},
		{"ListCapabilities", "first (no name) in cluster c", func() any {
			return &eks.ListCapabilitiesOutput{Capabilities: []ekstypes.CapabilitySummary{{}}}
		}},
		{"ListFargateProfiles", "first (no name) in cluster c", func() any { return &eks.ListFargateProfilesOutput{FargateProfileNames: []string{""}} }},
		{"ListIdentityProviderConfigs", "first (no name) in cluster c", func() any {
			return &eks.ListIdentityProviderConfigsOutput{IdentityProviderConfigs: []ekstypes.IdentityProviderConfig{{}}}
		}},
		{"ListNodegroups", "first (no name) in cluster c", func() any { return &eks.ListNodegroupsOutput{Nodegroups: []string{""}} }},
		{"ListPodIdentityAssociations", "first (no id) in cluster c", func() any {
			return &eks.ListPodIdentityAssociationsOutput{Associations: []ekstypes.PodIdentityAssociationSummary{{}}}
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "eks:"+c.op)
			calls := newEKSXCalls()
			h := calls.handlers()
			h[c.op] = func(any) (any, error) { return c.out(), nil }

			total, _, err := eksxScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, c.key)
			}
			if total != 26 {
				t.Errorf("total = %d; want 26", total)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanEKS_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeEKSAddon)
	calls := newEKSXCalls()
	calls.errs[calls.childID("addon", "c1", "addon-c1-t")] = eksDenied

	_, _, err := eksxScan(context.Background(), st, calls.handlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListCapabilities", "ListPodIdentityAssociations", "ListCertificateAuthorities"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// The associated access policies are listed for the access entries stored:
// an entry DescribeAccessEntry no longer finds is dropped and gets no
// listing.
func TestScanEKS_AccessPoliciesReadStoredEntriesOnly(t *testing.T) {
	st := newTestStore(t)
	calls := newEKSXCalls()
	calls.errs[calls.childID("access-entry", "c1", calls.principal("c1", "t"))] = eksGone
	h := calls.handlers()
	var policyCalls parentCalls
	h["ListAssociatedAccessPolicies"] = func(in any) (any, error) {
		p := in.(*eks.ListAssociatedAccessPoliciesInput)
		policyCalls.add(sv(p.ClusterName) + "|" + sv(p.PrincipalArn))
		return &eks.ListAssociatedAccessPoliciesOutput{}, nil
	}

	if _, _, err := eksxScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := sortedStrings("c1|"+calls.principal("c1", "b"), "c2|"+calls.principal("c2", "t"), "c2|"+calls.principal("c2", "b"))
	if got := policyCalls.distinct(); !slices.Equal(got, want) {
		t.Errorf("policies listed for %v; want %v", got, want)
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run.
func TestScanEKS_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newEKSXCalls()
	h := calls.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListFargateProfiles"]
	h["ListFargateProfiles"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := eksxScan(ctx, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("ListNodegroups"); n != 0 {
		t.Errorf("ListNodegroups called %d times after ctx ended", n)
	}
	// Fargate profiles are phase 5 of 11, so 6 never started.
	if !strings.Contains(err.Error(), "eks: 6 phases not run") {
		t.Errorf("err = %v; want it to say 6 phases not run", err)
	}
}

// A region without EKS (NXDOMAIN on ListClusters, nothing stored) is
// unavailable and makes no other call. An NXDOMAIN of a tag read after rows
// were stored is a failure of this scan: the counts are kept.
func TestScanEKS_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "eks.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newEKSXCalls()
		h := calls.handlers()
		h["ListClusters"] = func(any) (any, error) { return nil, nx }
		total, _, err := eksxScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListEksAnywhereSubscriptions") != 0 || calls.count("ListAddons") != 0 {
			t.Errorf("total = %d, ListEksAnywhereSubscriptions, ListAddons calls = %d, %d; want 0, 0, 0",
				total, calls.count("ListEksAnywhereSubscriptions"), calls.count("ListAddons"))
		}
	})
	t.Run("tag read after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		calls := newEKSXCalls()
		calls.errs[calls.childID("addon", "c1", "addon-c1-t")] = nx
		total, _, err := eksxScan(context.Background(), st, calls.handlers())
		assertGlueOutcome(t, err, outcomeError)
		if total != 30 {
			t.Errorf("total = %d; want 30", total)
		}
	})
}

func TestEKSChildKey(t *testing.T) {
	principal := "arn:aws-cn:iam::123456789012:role/a/b:c"
	for _, c := range []struct {
		nativeID, kind, cluster, key string
	}{
		{"arn:aws-cn:eks:cn-north-1:123456789012:access-entry/c1/" + principal, "access-entry", "c1", principal},
		{"arn:aws:eks:us-east-1:123456789012:identityproviderconfig/c1/oidc/idp", "identityproviderconfig", "c1", "oidc/idp"},
		{"arn:aws:eks:us-east-1:123456789012:addon/c1/a", "nodegroup", "", ""},
		{"not-an-arn", "addon", "", ""},
	} {
		if cluster, key := eksChildKey(c.nativeID, c.kind); cluster != c.cluster || key != c.key {
			t.Errorf("eksChildKey(%q, %q) = %q, %q; want %q, %q", c.nativeID, c.kind, cluster, key, c.cluster, c.key)
		}
	}
}
