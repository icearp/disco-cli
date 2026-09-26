package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Smithy JSON AST subset.
type smithyModel struct {
	Shapes map[string]*shape `json:"shapes"`
}

type shape struct {
	Type       string                     `json:"type"`
	Traits     map[string]json.RawMessage `json:"traits"`
	Members    map[string]*member         `json:"members"` // structure
	Member     *member                    `json:"member"`  // list
	Input      *ref                       `json:"input"`   // operation
	Output     *ref                       `json:"output"`
	Operations []ref                      `json:"operations"` // service
}

type member struct {
	Target string                     `json:"target"`
	Traits map[string]json.RawMessage `json:"traits"`
}

type ref struct {
	Target string `json:"target"`
}

// Service Reference document subset. Tags are camelCase for the repo lint
// rule; encoding/json matches object keys case-insensitively, so the
// catalog's PascalCase keys still decode.
type srDoc struct {
	Name    string `json:"name"`
	Actions []struct {
		Name        string `json:"name"`
		Annotations struct {
			Properties struct {
				IsList        bool `json:"isList"`
				IsWrite       bool `json:"isWrite"`
				IsTaggingOnly bool `json:"isTaggingOnly"`
			} `json:"properties"`
		} `json:"annotations"`
		Resources []struct {
			Name string `json:"name"`
		} `json:"resources"`
	} `json:"actions"`
	Operations []struct {
		Name              string                           `json:"name"`
		AuthorizedActions []struct{ Name, Service string } `json:"authorizedActions"`
	} `json:"operations"`
	Resources []struct {
		Name       string   `json:"name"`
		ARNFormats []string `json:"arnFormats"`
	} `json:"resources"`
}

// srService is one Service Reference document indexed for lookup.
type srService struct {
	actions   map[string]srAction
	opAction  map[string]string   // SDK operation name -> IAM action name
	resources map[string][]string // resource name -> ARN id variables beyond partition/region/account
	writeNoun map[string]bool     // Ident(noun of a lifecycle IsWrite action) -> true
	// mutableNoun holds the nouns only a setting verb writes (Update, Modify,
	// Enable, Export): a toggle, not a resource.
	mutableNoun map[string]bool
	resCanon    map[string]string   // Ident(resource name) -> resource name
	resNS       map[string]string   // Ident(resource name) -> the ARN's service namespace
	idStem      map[string][]string // stem of a resource's own ARN id variable ("bucket" from BucketName) -> resource names, sorted
	name        string              // the catalog document's own service name
	ops         map[string]bool     // every SDK operation name the document lists
}

type srAction struct {
	isList, isWrite bool
	resources       []string
}

var (
	// scopeParams are input members that shape paging or scope, never identity.
	// Keys are lower-cased: the models spell the account slot AccountId,
	// AwsAccountId and awsAccountId, and a case-sensitive lookup made
	// quicksight/* children of a non-existent quicksight/awsaccount.
	scopeParams = map[string]bool{"maxresults": true, "maxitems": true, "limit": true, "pagesize": true, "nexttoken": true, "marker": true, "pagetoken": true, "dryrun": true, "accountid": true, "awsaccountid": true, "region": true}
	idLikeRe    = regexp.MustCompile(`(Id|Ids|ID|IDs|Arn|Arns|ARN|ARNs|Name|Names|Identifier|Identifiers)$`)
	// idWordRe is the whole member: AppSync, DataZone, Bedrock, EKS, Grafana,
	// Lex and Cognito model their members `id`, `arn` and `name`, which the
	// PascalCase suffix rule never matches, so their child collections were
	// demoted to attributes as id-less (65 rows, four of them stored types).
	// It is deliberately anchored at both ends: a case-insensitive *suffix*
	// rule would swallow "domain" for "main" and "certificate" for "cate".
	idWordRe = regexp.MustCompile(`(?i)^(id|ids|arn|arns|name|names|identifier|identifiers)$`)
	// genericNoun is the noun that names no subject of its own.
	genericNoun = sdkinv.Ident("tag")
	// selfStems are id-like member stems that name the operation's own subject.
	selfStems = map[string]bool{"": true, sdkinv.Ident("resource"): true, sdkinv.Ident("target"): true}
	versionRe = regexp.MustCompile(`V\d+$`)
	// qualifierRe cuts the noun at a qualifier: "As" joins the list because
	// SearchProductsAsAdmin yielded the noun ProductsAsAdmin, which matched
	// neither the Service Reference resource nor a write noun. The offset>0
	// guard at the call site keeps ImportAsProvisionedProduct intact.
	qualifierRe = regexp.MustCompile(`(For|By|In|Of|Within|From|As)[A-Z]`)
	arnVarRe    = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)
	// arnMemberRe / createdRe are the evidence that a listed element is a
	// durable resource rather than a catalog row: it has an ARN, or it records
	// when it was created.
	arnMemberRe = regexp.MustCompile(`(?i)(^|[a-z0-9])arns?$`)
	createdRe   = regexp.MustCompile(`(?i)creat`)
	// descriptorRe names the shape of a listing member rather than its
	// subject: QueueUrls, InstanceIds, ClusterSummaries, TableDetails.
	descriptorRe = regexp.MustCompile(`(?i)(urls?|ids?|arns?|names?|summaries|summary|metadata|details?|list|infos?)$`)
	// crossCuttingTargets: a list action authorised against this many distinct
	// resource types reads a facet of them (tags, policies), not a resource.
	crossCuttingTargets = 3
	joinVerbs           = map[string]bool{"Associate": true, "Attach": true, "Register": true}
	// lifecycleVerbs create or destroy the noun. Only these make a noun a
	// resource; everything else changes an existing one.
	lifecycleVerbs = map[string]bool{"Create": true, "Delete": true, "Put": true, "Add": true, "Import": true, "Provision": true, "Allocate": true, "Register": true, "Associate": true, "Attach": true, "Copy": true, "Restore": true, "Launch": true, "Run": true, "Publish": true}
	listVerbs      = map[string]bool{"List": true, "Describe": true, "Get": true, "Search": true, "BatchGet": true}
	detailVerbs    = map[string]bool{"Get": true, "Describe": true, "Head": true}
)

type entry struct {
	service    string
	nouns      []string // Canon of every op noun seen, for the display name
	srName     string   // Canon of the catalog resource name when one matched
	depth      int
	parentID   string // Ident of the parent noun, "" at depth 0
	parentDisp string // display form of the parent noun when no entry resolves it
	// cands are every parent one of this entry's operations proposed, kept so
	// resolveTree can prefer one that is itself a candidate. place() picks the
	// shallowest for the pre-resolution depth; the catalog often names a
	// grandparent and a parent for one op set.
	cands   []parentCand
	class   sdkinv.Class
	rule    string // the classification rule that decided class
	signals map[string]bool
	ops     []sdkinv.Operation
	refs    map[string]bool
}

// parentCand is one operation's proposal for an entry's parent.
type parentCand struct {
	id, disp string
	depth    int // the lineage depth that proposal implies for this entry
}

// display is the noun shown in the key. With one spelling it is that noun
// singularised (a bare ListAliases). With several, a spelling that another
// spelling singularises to wins (analysis over analyses, app over apps), else
// the shortest. Identities (Ident) decide equality; display never does.
func (en *entry) display() string {
	spellings := map[string]bool{}
	for _, n := range append(en.nouns, en.srName) {
		if n != "" {
			spellings[n] = true
		}
	}
	best, bestSingular := "", ""
	for n := range spellings {
		if best == "" || shorter(n, best) {
			best = n
		}
		for q := range spellings {
			if q != n && sdkinv.Singular(q) == n && (bestSingular == "" || shorter(n, bestSingular)) {
				bestSingular = n
			}
		}
	}
	switch {
	case len(spellings) == 1:
		return sdkinv.Singular(best)
	case bestSingular != "":
		return bestSingular
	}
	// No spelling is another's singular: singularise the shortest rather than
	// ship it plural. Folding a legacy noun in (es/elasticsearchversions beside
	// es/versions) leaves exactly this case.
	return sdkinv.Singular(best)
}

func (extractor) Extract(_ context.Context, dir string) (*sdkinv.Universe, error) {
	u := &sdkinv.Universe{Provider: "aws", Pins: map[string]string{"aws-sdk-go-v2": sdkinv.AWSSDKRef}}
	sr, srVersion, err := loadServiceReference(filepath.Join(dir, "service-reference"))
	if err != nil {
		return nil, err
	}
	u.Pins["service-reference"] = srVersion
	// The catalog is unversioned and served live, so the fetched copy can be
	// newer than the pin. Report it rather than hide it: the pins printed on
	// every report and recorded in the baseline are what the numbers were
	// actually computed from.
	if srVersion != sdkinv.AWSServiceReferenceDigest {
		u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{
			Severity: "warn", Source: "service-reference/index.json",
			Message: fmt.Sprintf("catalog digest %s does not match the pin %s in internal/sdkinv/pins.go; the numbers are from the cached catalog", srVersion, sdkinv.AWSServiceReferenceDigest),
		})
	}
	modelDir := filepath.Join(dir, "repo", smithyModelsDir)
	files, err := filepath.Glob(filepath.Join(modelDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Smithy models under %s", modelDir)
	}
	entries := map[string]*entry{}
	words := map[string]map[string]bool{}   // service -> the words its own name is made of
	aliases := map[string]map[string]bool{} // Canon(spelling) -> services that answer to it
	for _, f := range files {
		raw, rerr := os.ReadFile(f)
		if rerr != nil {
			return nil, rerr
		}
		var m smithyModel
		if jerr := json.Unmarshal(raw, &m); jerr != nil {
			u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "warn", Source: filepath.Base(f), Message: jerr.Error()})
			continue
		}
		collectServiceWords(words, &m)
		collectServiceAliases(aliases, &m)
		diag, other := indexModel(entries, &m, sr, filepath.Base(f))
		if diag != "" {
			u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "warn", Source: filepath.Base(f), Message: diag})
		}
		u.Other = append(u.Other, other...)
	}
	mergeDetailReads(entries)
	foldLegacyNouns(entries, words)
	resolveTree(entries)
	u.Candidates = assemble(entries)
	u.ServiceAliases = serviceAliases(aliases, u.Candidates)
	sdkinv.SortCandidates(u.Candidates)
	sdkinv.SortOps(u.Other)
	return u, nil
}

// collectServiceAliases records every name a model's service trait gives it —
// arnNamespace, endpointPrefix and sdkId beside the signing name the
// candidates carry — which is how CloudFormation ("Pinpoint", "EMR") and the
// Service Reference ("cloudwatch") name the same service.
func collectServiceAliases(aliases map[string]map[string]bool, m *smithyModel) {
	svc, sdkID := serviceKey(m)
	if svc == "" {
		return
	}
	names := []string{sdkID}
	for n := range modelNamespaces(m, svc) {
		names = append(names, n)
	}
	for _, n := range names {
		if c := sdkinv.Canon(n); c != "" {
			if aliases[c] == nil {
				aliases[c] = map[string]bool{}
			}
			aliases[c][svc] = true
		}
	}
}

// serviceAliases keeps the unambiguous aliases of services that have
// candidates: a spelling that is itself a candidate service, or that two
// services answer to, names nothing reliably.
// A Service Reference document joined by operation names is an alias too.
func serviceAliases(aliases map[string]map[string]bool, cands []sdkinv.Candidate) map[string]string {
	primary := map[string]bool{}
	for _, c := range cands {
		primary[sdkinv.Canon(c.Service)] = true
		for _, sig := range c.Signals {
			if doc, ok := strings.CutPrefix(sig, "sr:document="); ok {
				c2 := sdkinv.Canon(doc)
				if aliases[c2] == nil {
					aliases[c2] = map[string]bool{}
				}
				aliases[c2][c.Service] = true
			}
		}
	}
	out := map[string]string{}
	for a, svcs := range aliases {
		if primary[a] || len(svcs) != 1 {
			continue
		}
		for svc := range svcs {
			if primary[sdkinv.Canon(svc)] {
				out[a] = svc
			}
		}
	}
	return out
}

// mergeDetailReads folds an attribute keyed <svc>/<parent>/<noun> into the
// resource <svc>/<noun> when that resource exists: the Get is the resource's
// own detail read, mis-nested only because the subject id was not
// recognisable (GetBasePathMapping's BasePath).
func mergeDetailReads(entries map[string]*entry) {
	for key, en := range entries {
		if en.class != sdkinv.ClassAttribute {
			continue
		}
		parts := strings.Split(key, "/")
		if len(parts) != 3 {
			continue
		}
		res := entries[parts[0]+"/"+parts[2]]
		if res == nil || res.class != sdkinv.ClassResource {
			continue
		}
		// The detail read's own spelling comes too: it is usually the singular
		// ("GetAlias" beside "ListAliases"), and dropping it left the key
		// plural whenever the catalog name was a compound ("function alias").
		res.nouns = append(res.nouns, en.nouns...)
		res.ops = append(res.ops, en.ops...)
		for sig := range en.signals {
			res.signals[sig] = true
		}
		for r := range en.refs {
			res.refs[r] = true
		}
		delete(entries, key)
	}
}

func loadServiceReference(dir string) (map[string]*srService, string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, "", err
	}
	out := map[string]*srService{}
	for _, f := range files {
		if filepath.Base(f) == "index.json" {
			continue
		}
		raw, rerr := os.ReadFile(f)
		if rerr != nil {
			return nil, "", rerr
		}
		var d srDoc
		if jerr := json.Unmarshal(raw, &d); jerr != nil {
			return nil, "", fmt.Errorf("%s: %w", f, jerr)
		}
		out[d.Name] = indexSR(&d)
	}
	if len(out) == 0 {
		return nil, "", fmt.Errorf("no Service Reference documents under %s", dir)
	}
	ver, err := serviceReferenceVersion(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, "", err
	}
	return out, ver, nil
}

// serviceReferenceVersion pins the unversioned catalog by the digest of its
// index, which is what sdkinv.AWSServiceReferenceDigest records. A date
// derived from the index's newest `modified` stamp looked like a pin but was
// a property of whenever the cache happened to be built: it moved on 12 of 13
// consecutive days, and a moved pin turns the ratchet's fatal checks into
// advisory ones (`gone-since-baseline` instead of `regressed`).
func serviceReferenceVersion(indexPath string) (string, error) {
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6]), nil
}

func indexSR(d *srDoc) *srService {
	s := &srService{actions: map[string]srAction{}, opAction: map[string]string{}, resources: map[string][]string{}, writeNoun: map[string]bool{}, mutableNoun: map[string]bool{}, resCanon: map[string]string{}, resNS: map[string]string{}, idStem: map[string][]string{}, name: d.Name, ops: map[string]bool{}}
	for _, a := range d.Actions {
		act := srAction{isList: a.Annotations.Properties.IsList, isWrite: a.Annotations.Properties.IsWrite}
		for _, r := range a.Resources {
			act.resources = append(act.resources, r.Name)
		}
		s.actions[a.Name] = act
		// Tagging-only writes (CreateTags) do not make "tag" a resource noun.
		if act.isWrite && !a.Annotations.Properties.IsTaggingOnly {
			verb, noun := splitVerb(a.Name)
			if !lifecycleVerbs[verb] {
				// Update, Modify, Enable, Set, Export, Start: the noun is
				// something you change, not something you create. Admitting it
				// as a resource made 134 settings and toggles permanent
				// denominator ballast (ec2/idformat via ModifyIdFormat,
				// eks/clusterversion via UpdateClusterVersion).
				s.mutableNoun[sdkinv.Ident(noun)] = true
				continue
			}
			s.writeNoun[sdkinv.Ident(noun)] = true
			// AssociateResolverRule creates a resolver rule *association*,
			// which is the noun its lister spells; stamping only the bare noun
			// left every association and attachment lister in the catalog
			// fallback, 18 rows outside the denominator and seven of them
			// stored by a disco scanner.
			if joinVerbs[verb] {
				s.writeNoun[sdkinv.Ident(noun+"association")] = true
				s.writeNoun[sdkinv.Ident(noun+"attachment")] = true
			}
		}
	}
	for _, o := range d.Operations {
		s.opAction[o.Name] = pickAction(d.Name, o.Name, o.AuthorizedActions)
		s.ops[o.Name] = true
	}
	for _, r := range d.Resources {
		var vars []string
		for _, f := range r.ARNFormats {
			all := arnVarRe.FindAllStringSubmatch(f, -1)
			var v []string
			for i, m := range all {
				// The last variable is the subject's own id whatever it is
				// named (organizations spells its account resource's id
				// ${AccountId}); every earlier partition/region/account
				// variable is the ARN's scope, not a level of nesting.
				if i < len(all)-1 && arnScopeVar(m[1]) {
					continue
				}
				v = append(v, m[1])
			}
			if len(v) > len(vars) {
				vars = v
			}
		}
		s.resources[r.Name] = vars
		if len(r.ARNFormats) > 0 { // a resource without an ARN is not addressable
			// The catalog lists singular and plural forms as separate resources
			// (RestApi, RestApis); both share one identity, the shorter names it.
			id := sdkinv.Ident(r.Name)
			if cur, ok := s.resCanon[id]; !ok || len(r.Name) < len(cur) {
				s.resCanon[id] = r.Name
			}
			if ns := arnNamespaceOf(r.ARNFormats[0]); ns != "" {
				s.resNS[id] = ns
			}
		}
		if len(vars) > 0 {
			// Several resources can share an id stem (glue's Job and
			// JobRun both end ${JobName}); keep them all and let the
			// operation's own noun choose, instead of the last read winning.
			if stem := memberStem(vars[len(vars)-1]); stem != "" {
				s.idStem[stem] = append(s.idStem[stem], r.Name)
			}
		}
	}
	for stem := range s.idStem {
		sort.Strings(s.idStem[stem])
	}
	return s
}

// stemResource names the catalog resource an id member refers to. With several
// sharing the stem, the operation's own noun decides, then the shortest name,
// so the pick is the same whatever order the catalog was read in.
func (s *srService) stemResource(stem, nounCanon string) string {
	names := s.idStem[stem]
	if len(names) == 0 {
		return ""
	}
	best := ""
	for _, n := range names {
		switch {
		case sdkinv.Ident(n) == nounCanon:
			return n
		case best == "" || shorter(n, best):
			best = n
		}
	}
	return best
}

// modelNamespaces is every ARN namespace this model can legitimately own: its
// join key plus the sigv4, arnNamespace and endpointPrefix spellings. es and
// servicecatalog disagree with their own catalog ARNs, so this is a test for
// *foreign* namespaces, never an allowlist of services.
func modelNamespaces(m *smithyModel, svc string) map[string]bool {
	out := map[string]bool{svc: true}
	for _, sh := range m.Shapes {
		if sh.Type != "service" {
			continue
		}
		var svcTrait struct {
			ArnNamespace   string `json:"arnNamespace"`
			EndpointPrefix string `json:"endpointPrefix"`
		}
		if raw, ok := sh.Traits["aws.api#service"]; ok {
			_ = json.Unmarshal(raw, &svcTrait)
		}
		for _, n := range []string{svcTrait.ArnNamespace, svcTrait.EndpointPrefix} {
			if n != "" {
				out[strings.ToLower(n)] = true
			}
		}
	}
	return out
}

// modelOps is every operation name a Smithy model ships.
func modelOps(m *smithyModel) map[string]bool {
	out := map[string]bool{}
	for id, sh := range m.Shapes {
		if sh.Type == "operation" {
			out[shapeName(id)] = true
		}
	}
	return out
}

// matchSRByOps finds the catalog document that authorises every operation of a
// model, smallest first, so the join survives a service the catalog files
// under another name. Nil when no document covers the model, or when the model
// has no operations to match on.
func matchSRByOps(srAll map[string]*srService, ops map[string]bool) *srService {
	if len(ops) == 0 {
		return nil
	}
	var best *srService
	for _, name := range slices.Sorted(maps.Keys(srAll)) {
		sr := srAll[name]
		covered := true
		for op := range ops {
			if !sr.ops[op] {
				covered = false
				break
			}
		}
		if covered && (best == nil || len(sr.ops) < len(best.ops)) {
			best = sr
		}
	}
	return best
}

// arnScopeVar reports whether an ARN variable names the partition, region or
// account the resource lives in rather than a level above it. chime, datasync,
// sso and organizations spell the account slot ${AccountId}, which an exact
// match against "Account" left counting as a level.
func arnScopeVar(name string) bool {
	l := strings.ToLower(name)
	for _, suffix := range []string{"partition", "region", "account"} {
		if strings.HasSuffix(strings.TrimSuffix(strings.TrimSuffix(l, "id"), "name"), suffix) {
			return true
		}
	}
	return false
}

// arnNamespaceOf is the service segment of an ARN format
// ("arn:${Partition}:ec2:…" → "ec2"). ec2.json genuinely carries a resource
// named "group" whose ARN lives in the resource-groups namespace, and matching
// on the noun alone admitted ec2/group from it.
func arnNamespaceOf(format string) string {
	parts := strings.SplitN(format, ":", 4)
	if len(parts) < 3 {
		return ""
	}
	return strings.ToLower(parts[2])
}

// pickAction chooses the IAM action an SDK operation is authorised by when
// the catalog lists several (ListObjectsV2 → GetObjectAcl, ListBucket): same
// name, else same verb. An action sharing neither (apigateway's GET) says
// nothing about the operation, so none is chosen and the SDK shape decides.
func pickAction(service, op string, actions []struct{ Name, Service string }) string {
	verb, _ := splitVerb(op)
	best, bestRank := "", 0
	for _, a := range actions {
		if a.Service != service && len(actions) > 1 {
			continue
		}
		rank := 0
		switch {
		case a.Name == op:
			rank = 2
		case strings.HasPrefix(a.Name, verb):
			rank = 1
		}
		if rank > bestRank {
			best, bestRank = a.Name, rank
		}
	}
	return best
}

// splitVerb separates the leading verb from the noun of an operation name:
// "DescribeInstances" → ("Describe", "Instances"), "BatchGetProjects" →
// ("BatchGet", "Projects"), "ListTagsForResource" → ("List", "Tags").
func splitVerb(op string) (verb, noun string) {
	i := 1
	for i < len(op) && op[i] >= 'a' && op[i] <= 'z' {
		i++
	}
	verb, noun = op[:i], op[i:]
	if verb == "Batch" && noun != "" {
		v2, n2 := splitVerb(noun)
		verb, noun = verb+v2, n2
	}
	noun = versionRe.ReplaceAllString(noun, "")
	if loc := qualifierRe.FindStringIndex(noun); loc != nil && loc[0] > 0 {
		noun = noun[:loc[0]]
	}
	return verb, noun
}

func serviceKey(m *smithyModel) (string, string) {
	for _, sh := range m.Shapes {
		if sh.Type != "service" {
			continue
		}
		var sigv4 struct {
			Name string `json:"name"`
		}
		if raw, ok := sh.Traits["aws.auth#sigv4"]; ok {
			_ = json.Unmarshal(raw, &sigv4)
		}
		var svc struct {
			ArnNamespace   string `json:"arnNamespace"`
			EndpointPrefix string `json:"endpointPrefix"`
			SDKID          string `json:"sdkId"`
		}
		if raw, ok := sh.Traits["aws.api#service"]; ok {
			_ = json.Unmarshal(raw, &svc)
		}
		// Lower-cased: the Service Reference names its documents in lower case,
		// and one model spells its signing name IoTSecuredTunneling, which keyed
		// two candidates no type or registry entry could ever match.
		switch {
		case sigv4.Name != "":
			return strings.ToLower(sigv4.Name), svc.SDKID
		case svc.ArnNamespace != "":
			return strings.ToLower(svc.ArnNamespace), svc.SDKID
		default:
			return strings.ToLower(svc.EndpointPrefix), svc.SDKID
		}
	}
	return "", ""
}

// opIsLister decides whether an op enumerates a collection, recording which
// evidence decided it. The catalog's IsList is incomplete
// (backup-gateway:ListGateways, batch:DescribeComputeEnvironments carry
// false): a read verb over a collection output is a lister whatever the
// annotation says.
func opIsLister(act srAction, hasAct bool, o opShape, verb string, signals map[string]bool) bool {
	shapeList := listVerbs[verb] && o.listMembers >= 1
	if !hasAct {
		signals["fallback"] = true
		return shapeList
	}
	signals["sr:action"] = true
	isList := act.isList || (shapeList && !act.isWrite)
	if !act.isList && isList {
		signals["shape-list"] = true
	}
	return isList
}

func indexModel(entries map[string]*entry, m *smithyModel, srAll map[string]*srService, file string) (string, []sdkinv.Operation) {
	svc, _ := serviceKey(m)
	if svc == "" {
		return "no service shape with a signing name", nil
	}
	namespaces := modelNamespaces(m, svc)
	var other []sdkinv.Operation
	sr := srAll[svc]
	diag := ""
	srMatched := ""
	if sr == nil {
		// The catalog files 14 services under a name no Smithy signing name
		// produces (cloudwatch is "monitoring", cloudcontrol is filed under
		// cloudformation). A document that authorises every one of this
		// model's operations is that service's document whatever it is
		// called; the smallest such document wins, so a superset service
		// cannot claim a smaller one's model.
		sr = matchSRByOps(srAll, modelOps(m))
		switch {
		case sr != nil:
			srMatched = sr.name
			diag = fmt.Sprintf("service %q absent from the Service Reference; joined to %q by operation names", svc, sr.name)
		default:
			diag = fmt.Sprintf("service %q absent from the Service Reference; classification falls back to SDK shape", svc)
		}
	}
	module := fmt.Sprintf("aws-sdk-go-v2@%s/%s%s", sdkinv.AWSSDKRef, smithyModelsDir, file)
	// Sorted shape ids: a model can carry two operation shapes of one name in
	// different namespaces (healthlake), and map order then decided which one's
	// Required survived — two of three consecutive extractions disagreed.
	ids := make([]string, 0, len(m.Shapes))
	for id := range m.Shapes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	seenOps := map[string]bool{} // op name + module: the duplicate shapes above
	for _, id := range ids {
		sh := m.Shapes[id]
		if sh.Type != "operation" {
			continue
		}
		op := id[strings.LastIndex(id, "#")+1:]
		if seenOps[op] {
			continue
		}
		seenOps[op] = true
		verb, noun := splitVerb(op)
		nounCanon := sdkinv.Ident(noun)
		o := analyzeOp(m, sh, noun, nounCanon)
		if nounCanon == "" || nounCanon == genericNoun {
			// An op that is all verb (sagemaker:Search) names no collection;
			// keying it yields "sagemaker/", a key nothing can ever match.
			// "tag" is the same kind of non-subject as the "resource" and
			// "target" of selfStems: ListTagsForResources describes whatever
			// it is called with, and route53/tag reached the numerator
			// attributed to aws:route53:cidr-collection.
			other = append(other, sdkinv.Operation{Service: svc, Name: op, Label: svc + ":" + op, Required: o.required, Module: module})
			continue
		}
		signals := map[string]bool{}
		if o.wrapped {
			signals["wrapped-list"] = true
		}
		if srMatched != "" {
			signals["sr:document="+srMatched] = true
		}
		act, hasAct := srAction{}, false
		if sr != nil {
			act, hasAct = sr.actions[sr.opAction[op]]
			if !hasAct {
				act, hasAct = sr.actions[op]
			}
		}
		isList := opIsLister(act, hasAct, o, verb, signals)
		if !isList && (o.listMembers != 0 || !detailVerbs[verb]) {
			// Writes, actions, batch reads: never a candidate.
			other = append(other, sdkinv.Operation{Service: svc, Name: op, Label: svc + ":" + op, Required: o.required, Module: module})
			continue
		}
		lin := lineage(sr, act, hasAct, o, nounCanon, signals)
		// A child collection named without its parent misses the catalog:
		// ListVersionsByFunction yields "versions" while the Service Reference
		// names the resource "function version", which a sibling op spelling
		// "FunctionVersions" does match — one object, two candidate keys, the
		// second counted as a gap. Retry with the parent prefixed here, before
		// the key and the class are fixed, not at the srName assignment below.
		if sr != nil && lin.parent != "" && sr.resCanon[nounCanon] == "" {
			if joined := sdkinv.Ident(lin.parent + nounCanon); sr.resCanon[joined] != "" {
				nounCanon = joined
				signals["sr:parent-noun"] = true
			}
		}
		class, rule := classify(classIn{isList: isList, noun: noun, namespaces: namespaces}, o, sr, nounCanon, lin, signals)
		key := svc + "/" + nounCanon
		if class == sdkinv.ClassAttribute && lin.parent != "" && !lin.self {
			key = svc + "/" + lin.parent + "/" + nounCanon
		}
		en := entries[key]
		if en == nil {
			en = &entry{service: svc, depth: -1, signals: map[string]bool{}, refs: map[string]bool{}}
			entries[key] = en
		}
		en.nouns = append(en.nouns, sdkinv.Canon(noun))
		if sr != nil {
			if name, ok := sr.resCanon[nounCanon]; ok {
				en.srName = sdkinv.Canon(name)
			}
		}
		en.place(lin)
		en.admit(class, rule)
		for _, r := range refsOf(m, sh, nounCanon) {
			en.refs[r] = true
		}
		for s := range signals {
			en.signals[s] = true
		}
		en.ops = append(en.ops, sdkinv.Operation{
			Service: svc, Name: op, Label: svc + ":" + op, IsList: isList, Paged: o.paged,
			Required: o.required, Targets: lin.targets, Module: module,
		})
	}
	return diag, other
}

// place is where an operation's subject sits in the resource tree.
// admit folds one operation's classification in, keeping the rule that decided
// the class the entry ends up with. A stronger class replaces the rule; an
// equal class keeps the alphabetically first, so the row does not depend on
// the order the models were read in.
func (en *entry) admit(class sdkinv.Class, rule string) {
	switch stronger := sdkinv.StrongerClass(en.class, class); {
	case en.class == "" || stronger != en.class:
		en.class, en.rule = stronger, rule
	case class == en.class && (en.rule == "" || rule < en.rule):
		en.rule = rule
	}
}

// place folds one op's lineage into the entry. Several ops land on one key
// (ListResolvers, ListResolversByFunction); the shallowest lineage wins, ties
// by parent identity then by its display, so the pick does not follow the
// model's map order.
func (en *entry) place(lin place) {
	parent := ""
	if lin.depth > 0 && lin.parent != "" {
		parent = lin.parent
		en.cands = append(en.cands, parentCand{id: parent, disp: lin.parentDisp, depth: lin.depth})
	}
	switch {
	case en.depth < 0 || lin.depth < en.depth:
		en.depth, en.parentID, en.parentDisp = lin.depth, parent, lin.parentDisp
	case lin.depth == en.depth && parent != "" && (en.parentID == "" || parent < en.parentID):
		en.parentID, en.parentDisp = parent, lin.parentDisp
	case lin.depth == en.depth && parent != "" && parent == en.parentID && shorter(lin.parentDisp, en.parentDisp):
		en.parentDisp = lin.parentDisp
	}
}

type place struct {
	targets    []string // catalog resources the operation is authorised against
	self       bool     // one target is the subject itself
	depth      int
	parent     string // Ident of the parent noun, "" when unknown
	parentDisp string // display form of the parent noun
}

// lineage derives depth and parent. Catalog targets count only when the
// operation requires an id: a lister with no required input lists top-level
// resources and its targets are the listed resources themselves. The
// subject's own ARN is authoritative when the catalog names it; otherwise
// the deepest target is the parent. Without catalog targets, required
// members that name a catalogued resource's id (Bucket, VolumeId) or look
// like an id name the ancestors.
func lineage(sr *srService, act srAction, hasAct bool, o opShape, nounCanon string, signals map[string]bool) place {
	var lin place
	if hasAct && len(o.required) > 0 && len(act.resources) > 0 {
		lin.targets = append(lin.targets, act.resources...)
		sort.Strings(lin.targets)
		var parents []string
		for _, r := range act.resources {
			vars := sr.resources[r]
			if len(vars) == 0 {
				continue
			}
			if sdkinv.Ident(r) == nounCanon {
				lin.self = true
				lin.depth = len(vars) - 1
				if len(vars) > 1 {
					lin.parent, lin.parentDisp = memberStem(vars[len(vars)-2]), memberDisp(vars[len(vars)-2])
				}
				break
			}
			parents = append(parents, r)
		}
		if !lin.self && len(parents) > 0 {
			p := pickParent(sr, parents, o.required)
			lin.parent, lin.parentDisp = sdkinv.Ident(p), sdkinv.Canon(p)
			lin.depth = len(sr.resources[p])
		}
		return lin
	}
	disp := map[string]string{}
	for _, r := range o.required {
		stem := memberStem(r)
		res := ""
		if sr != nil {
			res = sr.stemResource(stem, nounCanon)
		}
		switch {
		case selfStems[stem] || stem == nounCanon:
			lin.self = true
		case jobHandle(sr, stem):
			// An asynchronous handle, not a resource: Rekognition's JobId
			// names a StartCelebrityRecognition call, and reading it as a
			// parent hung 19 result collections under a phantom job.
			signals["job-handle"] = true
		case res != "":
			lin.targets = append(lin.targets, stem)
			disp[stem] = sdkinv.Canon(res)
			lin.depth = max(lin.depth, len(sr.resources[res]))
		case idLike(r):
			lin.targets = append(lin.targets, stem)
			disp[stem] = memberDisp(r)
			lin.depth = max(lin.depth, 1)
		}
	}
	sort.Strings(lin.targets)
	if len(lin.targets) > 0 {
		lin.parent = lin.targets[len(lin.targets)-1]
		lin.parentDisp = disp[lin.parent]
		signals["required-id"] = true
	}
	return lin
}

// jobHandle reports whether an id member names an asynchronous job the catalog
// does not publish as a resource. The stem must be exactly "job": glue's
// JobRun and batch's JobQueue are real catalogued resources.
func jobHandle(sr *srService, stem string) bool {
	return stem == sdkinv.Ident("job") && (sr == nil || sr.resCanon[stem] == "")
}

// nounStem is a member or noun with its descriptor suffix removed, as an
// identity: "QueueUrls" → "queue", "ClusterSummaries" → "cluster". The suffix
// only strips when something is left, so "Names" stays "name".
func nounStem(name string) string {
	stripped := descriptorRe.ReplaceAllString(name, "")
	if stripped == "" {
		return sdkinv.Ident(name)
	}
	return sdkinv.Ident(stripped)
}

// idLike reports whether a member name says "this is an identifier": the
// PascalCase suffix, or the whole member in any case.
func idLike(name string) bool { return idLikeRe.MatchString(name) || idWordRe.MatchString(name) }

// memberStem is the identity of the noun a member or ARN variable names:
// "KeyId" → Ident("key"), "Name" → "", "id" → "". memberDisp is its display
// form.
func memberStem(name string) string { return sdkinv.Ident(stripID(name)) }
func memberDisp(name string) string { return sdkinv.Canon(stripID(name)) }

func stripID(name string) string {
	if idWordRe.MatchString(name) {
		return ""
	}
	return idLikeRe.ReplaceAllString(name, "")
}

type opShape struct {
	required    []string
	paged       bool
	listMembers int  // output members that are collections (structs, or id-named primitives like TableNames)
	hasIDs      bool // some collection element carries an id-like member
	elemARN     bool // some collection element carries an ARN member
	elemTime    bool // some collection element carries a creation timestamp
	wrapped     bool // the collection sits one level down, inside a single payload structure
	namesNoun   bool // one collection is the operation's own noun
}

func analyzeOp(m *smithyModel, sh *shape, noun, nounCanon string) opShape {
	var o opShape
	_, o.paged = sh.Traits["smithy.api#paginated"]
	if sh.Input != nil {
		if in := m.Shapes[sh.Input.Target]; in != nil {
			for name, mem := range in.Members {
				if _, req := mem.Traits["smithy.api#required"]; req && !scopeParams[strings.ToLower(name)] {
					o.required = append(o.required, name)
				}
			}
			sort.Strings(o.required)
		}
	}
	if sh.Output == nil {
		return o
	}
	out := m.Shapes[sh.Output.Target]
	if out == nil {
		return o
	}
	o.scanCollections(m, out, nounCanon)
	if o.listMembers > 0 {
		return o
	}
	// A wrapped payload: GetApps answers ApplicationsResponse{Item []}. Only
	// the op's own collection counts — a blind descent turns all 399
	// single-structure read outputs into listings — so the noun must be plural
	// or the inner collection must be this noun's.
	inner := soleStructure(m, out)
	if inner == nil {
		return o
	}
	probe := opShape{}
	probe.scanCollections(m, inner, nounCanon)
	if probe.listMembers == 0 || (sdkinv.CanonSingular(noun) == sdkinv.Canon(noun) && !probe.namesNoun) {
		return o
	}
	probe.required, probe.paged, probe.wrapped = o.required, o.paged, true
	return probe
}

// scanCollections records what a payload structure's list members say about
// the subject: how many there are, whether their elements carry ids, an ARN or
// a creation timestamp, and whether one of them is this operation's own noun.
func (o *opShape) scanCollections(m *smithyModel, out *shape, nounCanon string) {
	for name, mem := range out.Members {
		t := m.Shapes[mem.Target]
		if t == nil || t.Type != "list" || t.Member == nil {
			continue
		}
		el := m.Shapes[t.Member.Target]
		switch {
		case el != nil && el.Type == "structure":
			o.listMembers++
			for f, fm := range el.Members {
				o.hasIDs = o.hasIDs || idLike(f)
				o.elemARN = o.elemARN || arnMemberRe.MatchString(f)
				if ft := m.Shapes[fm.Target]; ft != nil && ft.Type == "timestamp" && createdRe.MatchString(f) {
					o.elemTime = true
				}
			}
		case idLike(name) || nounStem(name) == nounCanon:
			// sqs:ListQueues answers QueueUrls []string: a collection of
			// primitives whose member name is not id-like but is this noun.
			o.listMembers++
			o.hasIDs = true
		default:
			continue
		}
		o.namesNoun = o.namesNoun || nounStem(name) == nounCanon || (el != nil && sdkinv.Ident(shapeName(t.Member.Target)) == nounCanon)
	}
}

// soleStructure is the one structure member of a payload, or nil when the
// payload has none or several.
func soleStructure(m *smithyModel, out *shape) *shape {
	var only *shape
	for _, mem := range out.Members {
		t := m.Shapes[mem.Target]
		if t == nil || t.Type != "structure" {
			continue
		}
		if only != nil {
			return nil
		}
		only = t
	}
	return only
}

// ownsNamespace reports whether an ARN namespace belongs to this model. A
// namespace that is a prefix of one of the model's own (or the other way
// round) is the same family under a longer name --
// route53-recovery-control-config's safety rules carry
// route53-recovery-control ARNs -- and only an unrelated namespace is foreign.
func ownsNamespace(own map[string]bool, ns string) bool {
	for n := range own {
		if strings.HasPrefix(n, ns) || strings.HasPrefix(ns, n) {
			return true
		}
	}
	return false
}

// classIn carries what classify needs about the operation itself.
type classIn struct {
	isList     bool
	noun       string          // the operation's noun as the SDK spells it
	namespaces map[string]bool // ARN namespaces this model owns
}

// foreignTargets counts the catalog resources an operation is authorised
// against that are neither its own subject nor an ancestor its lineage names.
// Counting all of them demoted real children: identitystore's
// ListGroupMemberships is authorised against AllGroupMemberships, Group and
// Identitystore, which is the membership, its parent and the store.
func foreignTargets(lin place, nounCanon string) int {
	n := 0
	for _, t := range lin.targets {
		id := sdkinv.Ident(t)
		if id == nounCanon || id == lin.parent || strings.HasSuffix(nounCanon, id) {
			continue
		}
		n++
	}
	return n
}

// shapeName is a Smithy shape id's local name ("com.amazonaws.sqs#Queue" → "Queue").
func shapeName(id string) string { return id[strings.LastIndex(id, "#")+1:] }

// classify decides the class and names the rule that decided it. The rule
// ships on the candidate so a report can state what its denominator admits.
func classify(cl classIn, o opShape, sr *srService, nounCanon string, lin place, signals map[string]bool) (sdkinv.Class, string) {
	if !cl.isList {
		signals["detail-read"] = true
		return sdkinv.ClassAttribute, "detail-read"
	}
	if sr != nil {
		// The catalog names the subject, not the payload: ListClusterSummaries
		// is a listing of clusters. Try the spelled noun first so the signal
		// records the exact match when there is one. This comes before the
		// cross-cutting test: a depth-2 resource is legitimately authorised
		// against its grandparent, its parent and itself.
		for _, n := range []string{nounCanon, nounStem(nounCanon)} {
			name, ok := sr.resCanon[n]
			if !ok {
				continue
			}
			if ns := sr.resNS[n]; ns != "" && !ownsNamespace(cl.namespaces, ns) {
				// The catalog resource is another service's: ec2.json carries
				// a "group" whose ARN is resource-groups'.
				signals["sr:foreign-namespace="+ns] = true
				continue
			}
			signals["sr:resource="+name] = true
			return sdkinv.ClassResource, "sr-resource"
		}
	}
	// Cross-cutting counts the resources an operation reaches *besides* its own
	// subject and the ancestors its lineage already names.
	if others := foreignTargets(lin, nounCanon); others >= crossCuttingTargets {
		signals["cross-cutting"] = true
		return sdkinv.ClassAttribute, "cross-cutting"
	}
	// A single-subject read the catalog does not call a listing, over a noun
	// that is already singular, reads one parent's setting: GetFunctionConfiguration,
	// DescribeOrganizationConfiguration. The shape-list override is load-bearing
	// (516 covered rows) and stays; only this corner of it is sub-state.
	if signals["shape-list"] && lin.self && sdkinv.CanonSingular(cl.noun) == sdkinv.Canon(cl.noun) {
		signals["single-subject-read"] = true
		return sdkinv.ClassAttribute, "single-subject-read"
	}
	if lin.depth > 0 {
		if !o.hasIDs {
			signals["id-less-collection"] = true
			return sdkinv.ClassAttribute, "id-less-collection"
		}
		signals["child-uncatalogued"] = true
		return sdkinv.ClassResource, "child-uncatalogued"
	}
	if sr != nil && sr.writeNoun[nounCanon] {
		signals["writable-noun"] = true
		return sdkinv.ClassResource, "writable-noun"
	}
	if sr != nil && sr.mutableNoun[nounCanon] {
		signals["mutable"] = true
	}
	if o.listMembers == 0 {
		signals["no-collection"] = true
		return sdkinv.ClassNonResource, "no-collection"
	}
	// Positive evidence beats the catalog fallback, which otherwise excludes
	// every listing of a service the Service Reference does not carry: an
	// element with its own ARN, or a record of when it was created, is a
	// resource the account owns, not a published catalog row.
	switch {
	case o.elemARN:
		signals["element-arn"] = true
		return sdkinv.ClassResource, "element-arn"
	case o.elemTime:
		signals["element-created"] = true
		return sdkinv.ClassResource, "element-created"
	}
	signals["read-only"] = true
	return sdkinv.ClassCatalog, "read-only"
}

// pickParent chooses the nearest ancestor among catalog targets: the deepest
// ARN, then the one a required input names.
func pickParent(sr *srService, targets, required []string) string {
	best, bestScore := "", -1
	for _, t := range targets {
		score := 2 * len(sr.resources[t])
		tc := sdkinv.Ident(t)
		for _, r := range required {
			if strings.Contains(sdkinv.Canon(r), tc) {
				score++
				break
			}
		}
		if score > bestScore {
			best, bestScore = t, score
		}
	}
	return best
}

// collectServiceWords records the words a service names itself by: the join
// key, the ARN namespace, the endpoint prefix and the sdkId's words.
// "Elasticsearch Service" is how es spells itself in the legacy model.
func collectServiceWords(words map[string]map[string]bool, m *smithyModel) {
	svc, sdkID := serviceKey(m)
	if svc == "" {
		return
	}
	if words[svc] == nil {
		words[svc] = map[string]bool{}
	}
	for n := range modelNamespaces(m, svc) {
		words[svc][sdkinv.Ident(n)] = true
	}
	for _, w := range strings.Fields(sdkID) {
		words[svc][sdkinv.Ident(w)] = true
	}
}

// foldLegacyNouns merges a candidate whose noun is the bare noun prefixed with
// the service's own name into that bare noun: elasticsearch-service.json and
// opensearch.json both sign as "es", so es/elasticsearchdomain and es/domain
// were two candidates for one set of domains, one of them permanently
// uncovered. Only a *service word* strips, never any shared prefix — stripping
// "Function" from Lambda's nouns would collide lambda/functionurlconfig with
// unrelated candidates.
func foldLegacyNouns(entries map[string]*entry, words map[string]map[string]bool) {
	for _, id := range slices.Sorted(maps.Keys(entries)) {
		en := entries[id]
		svc, noun, ok := strings.Cut(id, "/")
		if !ok || strings.Contains(noun, "/") {
			continue // attributes keep their parent segment
		}
		for _, w := range slices.Sorted(maps.Keys(words[svc])) {
			rest := strings.TrimPrefix(noun, w)
			target := entries[svc+"/"+rest]
			if w == "" || rest == noun || rest == "" || target == nil || target == en {
				continue
			}
			if entries[svc+"/"+w] != nil {
				// The word names a resource of this service too (connect has
				// contacts, bedrock has agents), so the prefix is part of the
				// noun, not the service's own name.
				continue
			}
			target.nouns = append(target.nouns, en.nouns...)
			target.ops = append(target.ops, en.ops...)
			target.admit(en.class, en.rule)
			target.signals["legacy-noun"] = true
			for sig := range en.signals {
				target.signals[sig] = true
			}
			for r := range en.refs {
				target.refs[r] = true
			}
			delete(entries, id)
			break
		}
	}
}

// resolveTree fixes parentage and depth once every entry exists. Indexing sees
// one operation at a time, so it can only propose the parent that operation's
// catalog targets name — often a grandparent, a scope slot or an asynchronous
// job handle, and 465 rows named a parent that was no candidate at all.
// Here the whole service is visible: a proposal that is itself an entry beats
// one that is not, the deepest such proposal wins, and an entry is never its
// own parent. Depth then follows the resolved parent rather than the ARN's
// variable count, which counts stack/${StackName}/${Id} as two levels.
func resolveTree(entries map[string]*entry) {
	for _, id := range slices.Sorted(maps.Keys(entries)) {
		en := entries[id]
		if en.parentID == "" {
			continue // some operation lists this resource top-level; that wins
		}
		self := id[strings.LastIndex(id, "/")+1:]
		best, bestScore := parentCand{}, -1
		for _, c := range en.cands {
			// Only the shallowest lineage's proposals: an operation that
			// reaches the resource through its grandparent must not deepen a
			// resource another operation lists one level up.
			if c.id == "" || c.id == self || c.depth != en.depth {
				continue
			}
			score := 0
			if p := entries[en.service+"/"+c.id]; p != nil {
				score = 2 + 2*p.depth
			}
			// Ties by the proposal's own depth, then by identity, so the pick
			// does not follow the order the models happened to be read in.
			if score > bestScore || (score == bestScore && (c.depth > best.depth || (c.depth == best.depth && c.id < best.id))) {
				best, bestScore = c, score
			}
		}
		if bestScore < 0 { // every proposal was the entry itself
			en.parentID, en.parentDisp, en.depth = "", "", 0
			continue
		}
		en.parentID, en.parentDisp = best.id, best.disp
	}
	// Depth from the resolved parent, memoised. A cycle (two entries naming
	// each other) keeps the indexed depths rather than looping.
	depth := map[string]int{}
	var resolve func(id string, seen map[string]bool) int
	resolve = func(id string, seen map[string]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		en := entries[id]
		d := en.depth
		if pid := en.service + "/" + en.parentID; en.parentID != "" && !seen[id] {
			if _, ok := entries[pid]; ok {
				seen[id] = true
				d = resolve(pid, seen) + 1
				delete(seen, id)
			}
		}
		depth[id] = d
		return d
	}
	for _, id := range slices.Sorted(maps.Keys(entries)) {
		entries[id].depth = resolve(id, map[string]bool{})
	}
}

// assemble turns identity-keyed entries into candidates keyed by display
// names. A parent renders as its own entry's display when one exists (so
// Parent always names a candidate key), else as the lineage's display form.
func assemble(entries map[string]*entry) []sdkinv.Candidate {
	byKey := map[string]*entry{}
	for _, id := range slices.Sorted(maps.Keys(entries)) { // fold order decides the kept entry
		en := entries[id]
		parts := strings.Split(id, "/")
		disp := en.display()
		key := en.service + "/" + disp
		if len(parts) == 3 { // attribute nested under its parent identity
			key = en.service + "/" + parentDisplay(entries, en) + "/" + disp
		}
		if dup := byKey[key]; dup != nil { // two identities with one display: fold the ops
			dup.admit(en.class, en.rule)
			dup.ops = append(dup.ops, en.ops...)
			for s := range en.signals {
				dup.signals[s] = true
			}
			for r := range en.refs {
				dup.refs[r] = true
			}
			continue
		}
		byKey[key] = en
	}
	out := make([]sdkinv.Candidate, 0, len(byKey))
	for key, en := range byKey {
		c := sdkinv.Candidate{Provider: "aws", Service: en.service, Key: key, Depth: en.depth, Class: en.class, Rule: en.rule, Ops: en.ops}
		if en.depth > 0 && en.parentID != "" {
			// Two identities can render to one display and fold together here,
			// which turned apigateway/methodresponse into its own parent.
			if p := en.service + "/" + parentDisplay(entries, en); p != key {
				c.Parent = p
			}
		}
		for s := range en.signals {
			c.Signals = append(c.Signals, s)
		}
		// Sibling models share a signing name (docdb, neptune and rds all sign
		// as "rds"), so one candidate can carry ops from several SDK modules.
		// Splitting on endpointPrefix only separates some of them; saying so on
		// the row is what a reader needs — three covered rows (lex/bot,
		// lex/botalias, lex/botversion) rest on Lex Classic ops folded in
		// beside lexmodelsv2's.
		if modules := opModules(c.Ops); len(modules) > 1 {
			c.Signals = append(c.Signals, "multi-module:"+strings.Join(modules, ","))
		}
		sort.Strings(c.Signals)
		for r := range en.refs {
			c.Refs = append(c.Refs, r)
		}
		sort.Strings(c.Refs)
		sdkinv.SortOps(c.Ops)
		out = append(out, c)
	}
	return out
}

// opModules is the distinct SDK modules a candidate's operations come from,
// named by their model file, sorted.
func opModules(ops []sdkinv.Operation) []string {
	seen := map[string]bool{}
	for _, o := range ops {
		seen[o.Module[strings.LastIndex(o.Module, "/")+1:]] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// shorter orders display forms: fewer characters first, then lexically.
func shorter(a, b string) bool { return len(a) < len(b) || (len(a) == len(b) && a < b) }

func parentDisplay(entries map[string]*entry, en *entry) string {
	if p := entries[en.service+"/"+en.parentID]; p != nil {
		return p.display()
	}
	return en.parentDisp
}
