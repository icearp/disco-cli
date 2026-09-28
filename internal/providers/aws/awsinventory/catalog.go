package awsinventory

import (
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
		SDK               []struct{ Name string }          `json:"sdk"`
	} `json:"operations"`
	Resources []struct {
		Name       string   `json:"name"`
		ARNFormats []string `json:"arnFormats"`
	} `json:"resources"`
}

// srService is one Service Reference document indexed for lookup.
type srService struct {
	actions map[string]srAction
	// opActions is every action authorising an SDK operation that the
	// document's own service owns, or the one foreign action when that is all
	// there is.
	opActions map[string][]string
	levels    map[string][]string // resource name -> ARN id variables of its resource field
	levelStem map[string][]string // stem of a resource's own id variable -> resource names, sorted
	resCanon  map[string]string   // Ident(resource name) -> resource name
	resNS     map[string]string   // Ident(resource name) -> the ARN's service namespace
	name      string              // the catalog document's own service name
	ops       map[string]bool     // every SDK operation name the document lists
	sdkNames  map[string]bool     // SDK client names the document's operations are published under
}

type srAction struct {
	isList, isWrite, taggingOnly bool
	resources                    []string
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
	idWordRe  = regexp.MustCompile(`(?i)^(id|ids|arn|arns|name|names|identifier|identifiers)$`)
	versionRe = regexp.MustCompile(`V\d+$`)
	arnVarRe  = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)
	// arnMemberRe / createdRe are the evidence that a listed element is a
	// durable resource rather than a catalog row: it has an ARN, or it records
	// when it was created.
	arnMemberRe = regexp.MustCompile(`(?i)(^|[a-z0-9])arns?$`)
	createdRe   = regexp.MustCompile(`(?i)creat`)
	// crossCuttingTargets: a list action authorised against this many distinct
	// resource types reads a facet of them (tags, policies), not a resource.
	crossCuttingTargets = 3
)

type entry struct {
	service    string
	nouns      []string // Canon of every op noun seen, for the display name
	srName     string   // Canon of the catalog resource name when one matched
	depth      int
	parentID   string // Ident of the parent noun, "" at depth 0
	parentDisp string // display form of the parent noun when no entry resolves it
	declared   bool   // placed by a Smithy resource shape, see place
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
	depth    int  // the lineage depth that proposal implies for this entry
	weak     bool // proposed by a required id's name alone
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
	// ship it plural.
	return sdkinv.Singular(best)
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
		if res == nil || res.class == sdkinv.ClassAttribute {
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
// index, which is what ServiceReferenceDigest records. A date
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
	s := &srService{
		actions: map[string]srAction{}, opActions: map[string][]string{}, levels: map[string][]string{}, levelStem: map[string][]string{},
		resCanon: map[string]string{}, resNS: map[string]string{}, name: d.Name, ops: map[string]bool{}, sdkNames: map[string]bool{},
	}
	for _, a := range d.Actions {
		p := a.Annotations.Properties
		act := srAction{isList: p.IsList, isWrite: p.IsWrite, taggingOnly: p.IsTaggingOnly}
		for _, r := range a.Resources {
			act.resources = append(act.resources, r.Name)
		}
		s.actions[a.Name] = act
	}
	for _, o := range d.Operations {
		s.ops[o.Name] = true
		for _, a := range o.AuthorizedActions {
			if a.Service == d.Name || len(o.AuthorizedActions) == 1 {
				s.opActions[o.Name] = append(s.opActions[o.Name], a.Name)
			}
		}
		for _, sdk := range o.SDK {
			s.sdkNames[strings.ToLower(sdk.Name)] = true
		}
	}
	for _, r := range d.Resources {
		if len(r.ARNFormats) == 0 { // a resource without an ARN is not addressable
			continue
		}
		for _, f := range r.ARNFormats {
			if lv := arnLevels(f); len(lv) > len(s.levels[r.Name]) {
				s.levels[r.Name] = lv
			}
		}
		if lv := s.levels[r.Name]; len(lv) > 0 {
			if stem := memberStem(lv[len(lv)-1]); stem != "" {
				s.levelStem[stem] = append(s.levelStem[stem], r.Name)
			}
		}
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
	for stem := range s.levelStem {
		sort.Strings(s.levelStem[stem])
	}
	return s
}

// modelNamespaces is every ARN namespace this model can legitimately own: its
// join key plus the sigv4, arnNamespace and endpointPrefix spellings. es and
// servicecatalog disagree with their own catalog ARNs, so this is a test for
// *foreign* namespaces, never an allowlist of services.
func modelNamespaces(m *smithyModel, svc string) map[string]bool {
	out := map[string]bool{svc: true}
	for _, id := range slices.Sorted(maps.Keys(m.Shapes)) { // the first service shape, as newServiceModel picks
		sh := m.Shapes[id]
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

// modelModule is the Operation.Module of every operation in one model file.
func modelModule(file string) string {
	return fmt.Sprintf("aws-sdk-go-v2@%s/%s%s", SDKRef, smithyModelsDir, file)
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

func serviceKey(m *smithyModel) (string, string) {
	for _, id := range slices.Sorted(maps.Keys(m.Shapes)) { // the first service shape, as newServiceModel picks
		sh := m.Shapes[id]
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

// admit folds one operation's classification in, keeping the rule that decided
// the class the entry ends up with. A stronger class replaces the rule; an
// equal class keeps the stronger rule (ruleRank), then the alphabetically
// first, so the row does not depend on the order the models were read in.
func (en *entry) admit(class sdkinv.Class, rule string) {
	switch stronger := sdkinv.StrongerClass(en.class, class); {
	case en.class == "" || stronger != en.class:
		en.class, en.rule = stronger, rule
	case class == en.class && (en.rule == "" || ruleRank(rule) > ruleRank(en.rule) || ruleRank(rule) == ruleRank(en.rule) && rule < en.rule):
		en.rule = rule
	}
}

// ruleRank orders the extractor's own resource rules by the strength of their
// evidence, so an entry admitted several ways names the strongest: the model's
// resource shape, the catalog, then structural inference.
func ruleRank(rule string) int {
	return 1 + slices.Index([]string{"child-uncatalogued", "element-created", "element-arn", "element-written", "sr-resource", "smithy-resource"}, rule)
}

// place folds one op's lineage into the entry. Several ops land on one key
// (ListResolvers, ListResolversByFunction); the shallowest lineage wins, ties
// by parent identity then by its display, so the pick does not follow the
// model's map order.
func (en *entry) place(lin place) {
	// The model's own resource nesting outranks any placement inferred from
	// an operation: deadline's SearchWorkers hangs off Farm, Worker off Fleet.
	switch {
	case en.declared && !lin.declared:
		return
	case lin.declared && !en.declared:
		en.declared, en.depth, en.cands = true, -1, nil
	}
	parent := ""
	if lin.depth > 0 && lin.parent != "" {
		parent = lin.parent
		en.cands = append(en.cands, parentCand{id: parent, disp: lin.parentDisp, depth: lin.depth, weak: lin.weak})
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

// place is where an operation's subject sits in the resource tree.
type place struct {
	targets    []string // catalog resources the operation is authorised against
	self       bool     // one target is the subject itself
	depth      int
	parent     string // Ident of the parent noun, "" when unknown
	parentDisp string // display form of the parent noun
	weak       bool   // the parent is only a required id's name
	declared   bool   // a Smithy resource shape places the subject itself
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

// resolveTree fixes parentage and depth once every entry exists. Indexing sees
// one operation at a time, so it can only propose the parent that operation's
// catalog targets name — often a grandparent, a scope slot or an asynchronous
// job handle, and 465 rows named a parent that was no candidate at all.
// Here the whole service is visible: a proposal that is itself an entry beats
// one that is not, the deepest such proposal wins, and an entry is never its
// own parent. Depth then follows the resolved parent rather than the ARN's
// variable count, which counts stack/${StackName}/${Id} as two levels.
//
// A weak proposal — a required id's name and nothing more — that names neither
// an entry nor a catalogued resource is an asynchronous handle (a JobId, a
// TicketId), not a parent.
func resolveTree(entries map[string]*entry, catalogued map[string]map[string]bool) {
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
			if c.weak && entries[en.service+"/"+c.id] == nil && !catalogued[en.service][c.id] {
				en.signals["handle"] = true
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
		// An attribute nests under its parent's identity, unless resolveTree
		// dropped that parent as a handle: then it reads the service's state.
		if p := parentDisplay(entries, en); len(parts) == 3 && p != "" {
			key = en.service + "/" + p + "/" + disp
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
