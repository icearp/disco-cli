package awsinventory

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		"widgets/zone":                    {sdkinv.ClassCatalog, 0, "", []string{"DescribeZones"}},
		"widgets/accountsetting":          {sdkinv.ClassAttribute, 0, "", []string{"GetAccountSettings"}},
		// A primitive list named after the noun is a collection: WidgetTypes
		// is a published catalog, SprocketUrls the sqs:ListQueues shape (#1).
		"widgets/widgettype":   {sdkinv.ClassCatalog, 0, "", []string{"ListWidgetTypes"}},
		"widgets/sprocket":     {sdkinv.ClassResource, 0, "", []string{"ListSprockets"}},
		"widgets/widgethealth": {sdkinv.ClassNonResource, 0, "", []string{"ListWidgetHealth"}},
		"widgets/gadget":       {sdkinv.ClassResource, 0, "", []string{"DescribeGadgets"}},
		"widgets/gizmo":        {sdkinv.ClassResource, 0, "", []string{"ListGizmos"}},
		// Two spellings of the noun (aliases / alias) and of the parent
		// (gizmo / gizmos): the key keeps the "s" of alias and the parent
		// names the gizmo candidate.
		"widgets/alias": {sdkinv.ClassResource, 1, "widgets/gizmo", []string{"GetAlias", "ListAliases"}},
		// Tags carry no id of their own: an attribute of whatever they tag.
		"widgets/widget/tag": {sdkinv.ClassAttribute, 1, "widgets/widget", []string{"ListTagsForResource"}},
		// No catalog and no traits: the collection shape alone makes the
		// listing, and nothing says GetThing reads, so it is not folded in.
		"nosr/thing": {sdkinv.ClassCatalog, 0, "", []string{"ListThings"}},
		// gears declares Smithy resource shapes: the model's nesting places
		// Worker under Fleet although SearchWorkers hangs off Farm; Alias,
		// declared at the service, nests under Farm by its identifiers.
		"gears/farm":            {sdkinv.ClassResource, 0, "", []string{"BatchGetFarms", "GetFarm", "ListFarms"}},
		"gears/fleet":           {sdkinv.ClassResource, 1, "gears/farm", []string{"GetFleet", "ListFleets"}},
		"gears/worker":          {sdkinv.ClassResource, 2, "gears/fleet", []string{"ListWorkers", "SearchWorkers"}},
		"gears/alias":           {sdkinv.ClassResource, 1, "gears/farm", []string{"ListAliases"}},
		"gears/managedresource": {sdkinv.ClassResource, 0, "", []string{"ListManagedResources"}},
		// Tag{key,value} is what the tagging-only TagResource takes.
		"gears/tag": {sdkinv.ClassAttribute, 0, "", []string{"ListTagsForResource"}},
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
	if !slices.Contains(w.Signals, "sr:resource=widget") || slices.Contains(w.Signals, "smithy-resource") {
		t.Errorf("widget signals = %v", w.Signals)
	}
	if g := got["widgets/grant"]; !slices.Contains(g.Signals, "child-uncatalogued") || !slices.Equal(g.Ops[0].Targets, []string{"widget"}) {
		t.Errorf("grant = %+v", g)
	}
	if tg := got["widgets/widget/tag"]; !slices.Contains(tg.Signals, "noun:qualifier-cut") || !slices.Contains(tg.Signals, "id-less-collection") {
		t.Errorf("tag signals = %v", tg.Signals)
	}
	if gd := got["widgets/gadget"]; !slices.Contains(gd.Signals, "element-written") {
		t.Errorf("gadget signals = %v", gd.Signals)
	}
	if th := got["nosr/thing"]; !slices.Contains(th.Signals, "list:untraited") || !hasOther(u, "nosr:GetThing") {
		t.Errorf("nosr signals = %v, other = %v", th.Signals, otherLabels(u))
	}
	for key, want := range map[string]string{"gears/farm": "smithy-resource", "gears/worker": "smithy-resource", "gears/tag": "tagging"} {
		if got[key].Rule != want {
			t.Errorf("%s rule = %q, want %q (signals %v)", key, got[key].Rule, want, got[key].Signals)
		}
	}
	// BatchGetFarms says "GetFarms": the model's own verb opens the noun.
	if !slices.Contains(got["gears/farm"].Signals, "key:verb-prefix") {
		t.Errorf("gears/farm signals = %v", got["gears/farm"].Signals)
	}
	if !hasOther(u, "gears:TagResource") || !hasOther(u, "gears:CreateFarm") {
		t.Errorf("gears writes not in Other: %v", otherLabels(u))
	}
	// An operation outside the service closure is accounted for as dropped,
	// never listed.
	if len(u.Dropped) != 1 || u.Dropped[0].Op.Name != "ListOrphans" || u.Dropped[0].Reason != "unreachable-from-service" {
		t.Errorf("dropped = %+v", u.Dropped)
	}
	// The fixture catalog is not the pinned one, so the pin mismatch is
	// expected here; what matters is that it is reported rather than silent.
	kinds := map[string]bool{}
	for _, d := range u.Diagnostics {
		kinds[d.Source] = true
	}
	if len(u.Diagnostics) != 2 || !kinds["nosr.json"] || !kinds["service-reference/index.json"] {
		t.Errorf("diagnostics = %+v", u.Diagnostics)
	}
	if u.Pins["aws-sdk-go-v2"] != SDKRef || u.Pins["service-reference"] == "" {
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

func TestCutQualifier(t *testing.T) {
	cases := []struct {
		noun   string
		inputs []string
		want   string
	}{
		{"TagsForResource", []string{"ResourceArn"}, "Tags"},
		{"HostedZonesByVPC", []string{"VPCId", "VPCRegion"}, "HostedZones"},
		{"ResourcesForTagOption", []string{"TagOptionId"}, "Resources"},
		{"SourcesForS3TableIntegration", []string{"IntegrationArn"}, "Sources"},
		// No input names the tail: the noun is the subject whole.
		{"AdminAccountsForOrganization", []string{"MaxResults"}, "AdminAccountsForOrganization"},
		// An acronym never joins, and the subject keeps a word.
		{"DBClusterSnapshots", []string{"DBClusterIdentifier"}, "DBClusterSnapshots"},
		{"ForResource", []string{"ResourceArn"}, "ForResource"},
		// A short name word (Vpn, Key, Out) is not a joiner when the input
		// names the subject itself.
		{"ClientVpnEndpoints", []string{"ClientVpnEndpointIds"}, "ClientVpnEndpoints"},
		{"CustomKeyStores", []string{"CustomKeyStoreId"}, "CustomKeyStores"},
		{"TransitGatewayVpcAttachments", []string{"TransitGatewayAttachmentIds"}, "TransitGatewayVpcAttachments"},
		{"OptOutLists", []string{"OptOutListNames"}, "OptOutLists"},
		{"HITsForQualificationType", []string{"QualificationTypeId"}, "HITs"},
	}
	for _, c := range cases {
		if got := cutQualifier(c.noun, c.inputs); got != c.want {
			t.Errorf("cutQualifier(%q, %v) = %q; want %q", c.noun, c.inputs, got, c.want)
		}
	}
}

func TestCamelWords(t *testing.T) {
	cases := map[string]string{
		"HostedZonesByVPC": "Hosted Zones By VPC",
		"DBClusterId":      "DB Cluster Id",
		"S3TableBucket":    "S3 Table Bucket",
		"Tags":             "Tags",
		"HITsForType":      "HITs For Type",
	}
	for in, want := range cases {
		if got := strings.Join(camelWords(in), " "); got != want {
			t.Errorf("camelWords(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestBindAction(t *testing.T) {
	s := &srService{
		actions: map[string]srAction{
			"ListBucket":    {isList: true, resources: []string{"bucket"}},
			"GetObject":     {resources: []string{"object"}},
			"PutObject":     {isWrite: true, resources: []string{"object"}},
			"TagResource":   {isWrite: true, taggingOnly: true},
			"TagBucket":     {isWrite: true, taggingOnly: true},
			"CreateStandby": {isList: true, isWrite: true},
		},
		opActions: map[string][]string{
			"ListObjectsV2": {"ListBucket"},
			// The write first: a last-action-wins union would lose it.
			"CopyObject":    {"PutObject", "GetObject"},
			"TagEverything": {"TagResource", "TagBucket"},
			"TagAndWrite":   {"TagResource", "PutObject"},
		},
	}
	if a, ok, sig := s.bindAction("GetObject", "GetObject"); !ok || sig != "sr:iam-action" || a.isWrite {
		t.Errorf("iamAction not bound first: %+v %v %q", a, ok, sig)
	}
	if a, ok, _ := s.bindAction("ListObjectsV2", ""); !ok || !a.isList {
		t.Errorf("authorised action not bound: %+v %v", a, ok)
	}
	// One write among the authorising actions makes the operation a write.
	if a, _, _ := s.bindAction("CopyObject", ""); !a.isWrite || !slices.Equal(a.resources, []string{"object"}) {
		t.Errorf("union lost the write or duplicated targets: %+v", a)
	}
	// Tagging-only only when every authorising action is.
	if a, _, _ := s.bindAction("TagEverything", ""); !a.taggingOnly {
		t.Errorf("all-tagging union not tagging-only: %+v", a)
	}
	if a, _, _ := s.bindAction("TagAndWrite", ""); a.taggingOnly {
		t.Errorf("mixed union tagging-only: %+v", a)
	}
	// Bound by its own name when no operation entry lists it, and says so.
	if a, ok, sig := s.bindAction("CreateStandby", ""); !ok || sig != "sr:action-name" || !a.isList || !a.isWrite {
		t.Errorf("same-name action: %+v %v %q", a, ok, sig)
	}
	if _, ok, sig := s.bindAction("DeleteBucket", ""); ok || sig != "sr:unbound" {
		t.Errorf("unknown operation bound: %v %q", ok, sig)
	}
}

// TestEntryAdmit: an entry admitted several ways names its strongest rule,
// whatever order the operations arrive in.
func TestEntryAdmit(t *testing.T) {
	for _, order := range [][]string{{"child-uncatalogued", "smithy-resource"}, {"smithy-resource", "child-uncatalogued"}} {
		en := &entry{}
		for _, r := range order {
			en.admit(sdkinv.ClassResource, r)
		}
		if en.rule != "smithy-resource" {
			t.Errorf("admit(%v) rule = %q", order, en.rule)
		}
	}
	en := &entry{}
	en.admit(sdkinv.ClassAttribute, "detail-read")
	en.admit(sdkinv.ClassResource, "child-uncatalogued")
	if en.class != sdkinv.ClassResource || en.rule != "child-uncatalogued" {
		t.Errorf("stronger class did not take its rule: %s %s", en.class, en.rule)
	}
}

// TestExtract_Live anchors the derived rules against the real catalog; skipped
// when the cache is absent.
func TestExtract_Live(t *testing.T) {
	dir := filepath.Join(sdkinv.DefaultCacheRoot(), "aws@"+SDKRef)
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
		"iam/accountsummary":         {sdkinv.ClassNonResource, 0, ""},
		"lambda/function":            {sdkinv.ClassResource, 0, ""},
		"logs/loggroup":              {sdkinv.ClassResource, 0, ""},
		"rds/dbinstance":             {sdkinv.ClassResource, 0, ""},
		"eks/nodegroup":              {sdkinv.ClassResource, 1, "eks/cluster"},
		"route53/resourcerecordset":  {sdkinv.ClassResource, 1, "route53/hostedzone"},
		"dynamodb/table":             {sdkinv.ClassResource, 0, ""},
		"apigateway/restapi":         {sdkinv.ClassResource, 0, ""},
		// The model's own resource nesting places these (Worker sits under
		// Fleet although SearchWorkers hangs off Farm; FunctionAlias shares
		// Function's identifier).
		"deadline/worker":          {sdkinv.ClassResource, 2, "deadline/fleet"},
		"lambda/functionalias":     {sdkinv.ClassResource, 1, "lambda/function"},
		"connect/hoursofoperation": {sdkinv.ClassResource, 1, "connect/instance"},
		// Each keeps its own catalogued key though all four answer
		// MonitoringJobDefinitionSummary.
		"sagemaker/dataqualityjobdefinition":  {sdkinv.ClassResource, 0, ""},
		"sagemaker/modelqualityjobdefinition": {sdkinv.ClassResource, 0, ""},
		// Short name words are not joiners.
		"ec2/clientvpnendpoint":           {sdkinv.ClassResource, 0, ""},
		"ec2/transitgatewayvpcattachment": {sdkinv.ClassResource, 0, ""},
		// Paginated with no items path.
		"servicediscovery/instance": {sdkinv.ClassResource, 1, "servicediscovery/service"},
		"iam/role/roletag":          {sdkinv.ClassAttribute, 1, "iam/role"},
		// Published rows no write touches stay out of the denominator.
		"ec2/availabilityzone":          {sdkinv.ClassCatalog, 0, ""},
		"ec2/reservedinstancesoffering": {sdkinv.ClassCatalog, 0, ""},
	}
	// The names the registries use for a service, derived from the model's
	// own traits and the operation-name join (#117).
	for alias, svc := range map[string]string{"pinpoint": "mobiletargeting", "apigatewayv2": "apigateway", "cloudwatch": "monitoring", "emr": "elasticmapreduce"} {
		if got := u.ServiceAliases[alias]; got != svc {
			t.Errorf("ServiceAliases[%s] = %q, want %q", alias, got, svc)
		}
	}
	// A tag has no id of its own: never a resource, wherever it is listed.
	for k, c := range got {
		if strings.HasSuffix(k, "/tag") && c.Class == sdkinv.ClassResource {
			t.Errorf("%s is a resource: %v", k, c.Signals)
		}
	}
	// A report version is its report's version history, not a resource (the
	// Smithy model folds ListReportVersions into Report).
	if c, ok := got["artifact/reportversion"]; ok {
		t.Errorf("artifact/reportversion is a candidate: %+v", c)
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
	// Tag listings never fold into a resource through their shared Tag shape.
	for _, k := range []string{"iam/samlprovider", "iam/role"} {
		if slices.ContainsFunc(opNames(got[k]), func(o string) bool { return strings.HasSuffix(o, "Tags") }) {
			t.Errorf("%s carries tag ops: %v", k, opNames(got[k]))
		}
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

// TestServiceReferenceVersion: the unversioned catalog pins by the digest of
// its index and by nothing else. The `modified` stamps it used to read are a
// property of when the cache was built, not of the catalog's content, so two
// machines holding the same catalog disagreed on the pin and the ratchet
// downgraded its fatal checks.
func TestServiceReferenceVersion(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "index.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stamped := `[{"service":"a","modified":1774454984},{"service":"b","modified":1789999999}]`
	first, err := serviceReferenceVersion(write(stamped))
	if err != nil || len(first) != 12 {
		t.Fatalf("stamped index = %q, %v; want a 12-hex digest", first, err)
	}
	if again, _ := serviceReferenceVersion(write(stamped)); again != first {
		t.Errorf("digest not stable: %q vs %q", first, again)
	}
	// Same services, newer stamps: the same catalog content must not be a
	// different pin, and different content must be.
	restamped, _ := serviceReferenceVersion(write(`[{"service":"a","modified":1774454984},{"service":"b","modified":1799999999}]`))
	if restamped == first {
		t.Error("a changed index must change the digest")
	}
	unstamped, _ := serviceReferenceVersion(write(`[{"service":"widgets"}]`))
	if len(unstamped) != 12 || unstamped == first {
		t.Errorf("unstamped index = %q", unstamped)
	}
	if _, err := serviceReferenceVersion(filepath.Join(dir, "absent.json")); err == nil {
		t.Error("a missing index must error")
	}
}
