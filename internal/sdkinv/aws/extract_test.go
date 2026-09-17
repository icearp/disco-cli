package aws

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

type want struct {
	class  sdkinv.Class
	depth  int
	parent string
	ops    []string
}

func byKey(u *sdkinv.Universe) map[string]sdkinv.Candidate {
	m := make(map[string]sdkinv.Candidate, len(u.Candidates))
	for _, c := range u.Candidates {
		m[c.Key] = c
	}
	return m
}

func opNames(c sdkinv.Candidate) []string {
	out := make([]string, 0, len(c.Ops))
	for _, o := range c.Ops {
		out = append(out, o.Name)
	}
	slices.Sort(out)
	return out
}

func TestExtract_Fixture(t *testing.T) {
	u, err := extractor{}.Extract(context.Background(), filepath.Join("testdata", "cache"))
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(u)
	wants := map[string]want{
		"widgets/widget":                  {sdkinv.ClassResource, 0, "", []string{"GetWidget", "ListWidgets", "ListWidgetsV2"}},
		"widgets/widget/widgetencryption": {sdkinv.ClassAttribute, 1, "widgets/widget", []string{"GetWidgetEncryption"}},
		"widgets/grant":                   {sdkinv.ClassResource, 1, "widgets/widget", []string{"ListGrants"}},
		"widgets/widget/tag":              {sdkinv.ClassAttribute, 1, "widgets/widget", []string{"ListTagsForResource"}},
		"widgets/zone":                    {sdkinv.ClassCatalog, 0, "", []string{"DescribeZones"}},
		"widgets/accountsetting":          {sdkinv.ClassAttribute, 0, "", []string{"GetAccountSettings"}},
		"widgets/widgettype":              {sdkinv.ClassNonResource, 0, "", []string{"ListWidgetTypes"}},
		"widgets/gadget":                  {sdkinv.ClassResource, 0, "", []string{"DescribeGadgets"}},
		"widgets/gizmo":                   {sdkinv.ClassResource, 0, "", []string{"ListGizmos"}},
		// Two spellings of the noun (aliases / alias) and of the parent
		// (gizmo / gizmos): the key keeps the "s" of alias and the parent
		// names the gizmo candidate.
		"widgets/alias": {sdkinv.ClassResource, 1, "widgets/gizmo", []string{"GetAlias", "ListAliases"}},
		"nosr/thing":    {sdkinv.ClassCatalog, 0, "", []string{"GetThing", "ListThings"}},
	}
	if len(got) != len(wants) {
		t.Errorf("candidates = %d, want %d: %v", len(got), len(wants), slices.Sorted(mapsKeys(got)))
	}
	for k, w := range wants {
		c, ok := got[k]
		if !ok {
			t.Errorf("missing candidate %s", k)
			continue
		}
		if c.Class != w.class || c.Depth != w.depth || c.Parent != w.parent || !slices.Equal(opNames(c), w.ops) {
			t.Errorf("%s = class %s depth %d parent %q ops %v signals %v; want %+v", k, c.Class, c.Depth, c.Parent, opNames(c), c.Signals, w)
		}
	}
	// Refs: id-like members below the element minus its own id (WidgetId,
	// Name), through one nested structure.
	if got, want := got["widgets/widget"].Refs, []string{"Network.SubnetIds", "VpcId"}; !slices.Equal(got, want) {
		t.Errorf("widgets/widget refs = %v, want %v", got, want)
	}
	if r := got["widgets/gadget"].Refs; r != nil {
		t.Errorf("widgets/gadget refs = %v, want none (only its own arn)", r)
	}
	// Write ops never become candidates; they ship as Other for pairing.
	for _, k := range []string{"widgets/createwidget", "widgets/creategadget"} {
		if _, ok := got[k]; ok {
			t.Errorf("write op surfaced as candidate %s", k)
		}
	}
	if !hasOther(u, "widgets:CreateWidget") || hasOther(u, "widgets:ListWidgets") {
		t.Errorf("Other = %v", otherLabels(u))
	}
	w := got["widgets/widget"]
	for _, o := range w.Ops {
		if o.Label != "widgets:"+o.Name || o.Module == "" {
			t.Errorf("op = %+v", o)
		}
		if (o.Name == "ListWidgets") != (o.Paged && o.IsList && len(o.Required) == 0) && o.Name != "ListWidgetsV2" {
			t.Errorf("ListWidgets shape: %+v", o)
		}
	}
	if !slices.Contains(w.Signals, "sr:resource=widget") || slices.Contains(w.Signals, "fallback") {
		t.Errorf("widget signals = %v", w.Signals)
	}
	if g := got["widgets/grant"]; !slices.Contains(g.Signals, "child-uncatalogued") || !slices.Equal(g.Ops[0].Targets, []string{"widget"}) {
		t.Errorf("grant = %+v", g)
	}
	if tg := got["widgets/widget/tag"]; !slices.Contains(tg.Signals, "cross-cutting") {
		t.Errorf("tag signals = %v", tg.Signals)
	}
	if gd := got["widgets/gadget"]; !slices.Contains(gd.Signals, "writable-noun") {
		t.Errorf("gadget signals = %v", gd.Signals)
	}
	if th := got["nosr/thing"]; !slices.Contains(th.Signals, "fallback") {
		t.Errorf("nosr signals = %v", th.Signals)
	}
	if len(u.Diagnostics) != 1 || u.Diagnostics[0].Source != "nosr.json" {
		t.Errorf("diagnostics = %+v", u.Diagnostics)
	}
	if u.Pins["aws-sdk-go-v2"] != sdkinv.AWSSDKRef || u.Pins["service-reference"] == "" {
		t.Errorf("pins = %v", u.Pins)
	}
}

func mapsKeys(m map[string]sdkinv.Candidate) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func TestSplitVerb(t *testing.T) {
	cases := map[string][2]string{
		"DescribeInstances":                {"Describe", "Instances"},
		"ListObjectsV2":                    {"List", "Objects"},
		"ListTagsForResource":              {"List", "Tags"},
		"BatchGetProjects":                 {"BatchGet", "Projects"},
		"ListDistributionsByWebACL":        {"List", "Distributions"},
		"GetBucketPolicy":                  {"Get", "BucketPolicy"},
		"ListForms":                        {"List", "Forms"},
		"DescribeFleetLocationUtilization": {"Describe", "FleetLocationUtilization"},
	}
	for in, w := range cases {
		if v, n := splitVerb(in); v != w[0] || n != w[1] {
			t.Errorf("splitVerb(%q) = (%q, %q); want %v", in, v, n, w)
		}
	}
}

func TestPickAction(t *testing.T) {
	acts := func(names ...string) []struct{ Name, Service string } {
		out := make([]struct{ Name, Service string }, 0, len(names))
		for _, n := range names {
			out = append(out, struct{ Name, Service string }{n, "s3"})
		}
		return out
	}
	if got := pickAction("s3", "ListObjectsV2", acts("GetObjectAcl", "ListBucket")); got != "ListBucket" {
		t.Errorf("same-verb action not preferred: %q", got)
	}
	if got := pickAction("s3", "GetObject", acts("ListBucket", "GetObject")); got != "GetObject" {
		t.Errorf("same-name action not preferred: %q", got)
	}
	if got := pickAction("apigateway", "GetRestApis", acts("GET")); got != "" {
		t.Errorf("generic action bound: %q", got)
	}
	other := []struct{ Name, Service string }{{"ListBucket", "s3-object-lambda"}, {"ListBucket", "s3"}}
	if got := pickAction("s3", "ListObjects", other); got != "ListBucket" {
		t.Errorf("cross-service action chosen: %q", got)
	}
}

// TestExtract_Live anchors the derived rules against the real catalog; skipped
// when the cache is absent.
func TestExtract_Live(t *testing.T) {
	dir := filepath.Join(sdkinv.DefaultCacheRoot(), "aws@"+sdkinv.AWSSDKRef)
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Skip("aws SDK cache not fetched")
	}
	u, err := extractor{}.Extract(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(u)
	anchors := map[string]struct {
		class  sdkinv.Class
		depth  int
		parent string
	}{
		"ec2/instance":               {sdkinv.ClassResource, 0, ""},
		"ec2/volume":                 {sdkinv.ClassResource, 0, ""},
		"kms/key":                    {sdkinv.ClassResource, 0, ""},
		"kms/grant":                  {sdkinv.ClassResource, 1, "kms/key"},
		"s3/bucket":                  {sdkinv.ClassResource, 0, ""},
		"s3/object":                  {sdkinv.ClassResource, 1, "s3/bucket"},
		"s3/bucket/bucketencryption": {sdkinv.ClassAttribute, 1, "s3/bucket"},
		"iam/role":                   {sdkinv.ClassResource, 0, ""},
		"iam/accountpasswordpolicy":  {sdkinv.ClassAttribute, 0, ""},
		"ec2/instancetype":           {sdkinv.ClassCatalog, 0, ""},
		"ec2/region":                 {sdkinv.ClassCatalog, 0, ""},
		"ec2/accountattribute":       {sdkinv.ClassCatalog, 0, ""},
		"ec2/tag":                    {sdkinv.ClassCatalog, 0, ""},
		"iam/accountsummary":         {sdkinv.ClassNonResource, 0, ""},
		"lambda/function":            {sdkinv.ClassResource, 0, ""},
		"logs/loggroup":              {sdkinv.ClassResource, 0, ""},
		"rds/dbinstance":             {sdkinv.ClassResource, 0, ""},
		"eks/nodegroup":              {sdkinv.ClassResource, 1, "eks/cluster"},
		"route53/resourcerecordset":  {sdkinv.ClassResource, 1, "route53/hostedzone"},
		"dynamodb/table":             {sdkinv.ClassResource, 0, ""},
		"apigateway/restapi":         {sdkinv.ClassResource, 0, ""},
	}
	for k, w := range anchors {
		c, ok := got[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		if c.Class != w.class || c.Depth != w.depth || c.Parent != w.parent {
			t.Errorf("%s = class %s depth %d parent %q signals %v; want %+v", k, c.Class, c.Depth, c.Parent, c.Signals, w)
		}
	}
	if o := got["s3/object"].Ops; !slices.Contains(opNames(got["s3/object"]), "ListObjectsV2") {
		t.Errorf("s3/object ops = %v", o)
	}
	if slices.Contains(opNames(got["iam/role"]), "CreateRole") {
		t.Error("write op attached to iam/role")
	}
	classes := map[sdkinv.Class]int{}
	services := map[string]bool{}
	for _, c := range u.Candidates {
		classes[c.Class]++
		services[c.Service] = true
	}
	t.Logf("aws candidates: %d %v across %d services; diagnostics %d", len(u.Candidates), classes, len(services), len(u.Diagnostics))
	if classes[sdkinv.ClassResource] < 2000 || len(services) < 300 {
		t.Errorf("universe too small: %v across %d services", classes, len(services))
	}
	if len(u.Diagnostics) > 30 {
		t.Errorf("too many services missing from the Service Reference: %d", len(u.Diagnostics))
	}
}

func hasOther(u *sdkinv.Universe, label string) bool {
	for _, o := range u.Other {
		if o.Label == label {
			return true
		}
	}
	return false
}

func otherLabels(u *sdkinv.Universe) []string {
	out := make([]string, 0, len(u.Other))
	for _, o := range u.Other {
		out = append(out, o.Label)
	}
	return out
}

// TestEntryPlace_OrderIndependent feeds the same lineages in both orders:
// the parent spelling an entry keeps must not follow the model's map order.
func TestEntryPlace_OrderIndependent(t *testing.T) {
	a := place{depth: 1, parent: "gizmo", parentDisp: "gizmos"}
	b := place{depth: 1, parent: "gizmo", parentDisp: "gizmo"}
	for _, order := range [][]place{{a, b}, {b, a}} {
		en := &entry{depth: -1}
		for _, lin := range order {
			en.place(lin)
		}
		if en.parentID != "gizmo" || en.parentDisp != "gizmo" {
			t.Errorf("order %v: parent %q display %q", order, en.parentID, en.parentDisp)
		}
	}
	en := &entry{depth: -1}
	en.place(place{depth: 2, parent: "deep", parentDisp: "deep"})
	en.place(place{depth: 1, parent: "shallow", parentDisp: "shallow"})
	en.place(place{depth: 1, parent: "another", parentDisp: "another"})
	if en.depth != 1 || en.parentID != "another" {
		t.Errorf("shallowest lineage then smallest parent should win: depth %d parent %q", en.depth, en.parentID)
	}
}

// TestServiceReferenceVersion: the pin follows the catalog's newest
// `modified` stamp (UTC date), not the file's mtime, and an index without
// stamps pins by digest so the fixture is still versioned.
func TestServiceReferenceVersion(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "index.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	got, err := serviceReferenceVersion(write(`[{"service":"a","modified":1774454984},{"service":"b","modified":1789999999}]`))
	if err != nil || got != "2026-09-21" {
		t.Errorf("stamped index = %q, %v; want 2026-09-21", got, err)
	}
	first, err := serviceReferenceVersion(write(`[{"service":"widgets"}]`))
	if err != nil || len(first) != 12 {
		t.Errorf("unstamped index = %q, %v; want a 12-hex digest", first, err)
	}
	if again, _ := serviceReferenceVersion(write(`[{"service":"widgets"}]`)); again != first {
		t.Errorf("digest not stable: %q vs %q", first, again)
	}
	if _, err := serviceReferenceVersion(write(`{}`)); err == nil {
		t.Error("non-array index must error")
	}
}
