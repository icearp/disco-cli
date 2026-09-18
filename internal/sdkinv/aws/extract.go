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
	writeNoun map[string]bool     // Ident(noun of an IsWrite action) -> true
	resCanon  map[string]string   // Ident(resource name) -> resource name
	idStem    map[string]string   // stem of a resource's own ARN id variable ("bucket" from BucketName) -> resource name
}

type srAction struct {
	isList, isWrite bool
	resources       []string
}

var (
	// scopeParams are input members that shape paging or scope, never identity.
	scopeParams = map[string]bool{"MaxResults": true, "MaxItems": true, "Limit": true, "PageSize": true, "NextToken": true, "Marker": true, "PageToken": true, "DryRun": true, "AccountId": true, "Region": true}
	idLikeRe    = regexp.MustCompile(`(Id|Ids|ID|IDs|Arn|Arns|ARN|ARNs|Name|Names|Identifier|Identifiers)$`)
	// selfStems are id-like member stems that name the operation's own subject.
	selfStems   = map[string]bool{"": true, sdkinv.Ident("resource"): true, sdkinv.Ident("target"): true}
	versionRe   = regexp.MustCompile(`V\d+$`)
	qualifierRe = regexp.MustCompile(`(For|By|In|Of|Within|From)[A-Z]`)
	arnVarRe    = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)
	// crossCuttingTargets: a list action authorised against this many distinct
	// resource types reads a facet of them (tags, policies), not a resource.
	crossCuttingTargets = 3
	listVerbs           = map[string]bool{"List": true, "Describe": true, "Get": true, "Search": true, "BatchGet": true}
	detailVerbs         = map[string]bool{"Get": true, "Describe": true, "Head": true}
)

type entry struct {
	service    string
	nouns      []string // Canon of every op noun seen, for the display name
	srName     string   // Canon of the catalog resource name when one matched
	depth      int
	parentID   string // Ident of the parent noun, "" at depth 0
	parentDisp string // display form of the parent noun when no entry resolves it
	class      sdkinv.Class
	signals    map[string]bool
	ops        []sdkinv.Operation
	refs       map[string]bool
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
	return best
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
		diag, other := indexModel(entries, &m, sr, filepath.Base(f))
		if diag != "" {
			u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "warn", Source: filepath.Base(f), Message: diag})
		}
		u.Other = append(u.Other, other...)
	}
	mergeDetailReads(entries)
	u.Candidates = assemble(entries)
	sdkinv.SortCandidates(u.Candidates)
	sdkinv.SortOps(u.Other)
	return u, nil
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
	s := &srService{actions: map[string]srAction{}, opAction: map[string]string{}, resources: map[string][]string{}, writeNoun: map[string]bool{}, resCanon: map[string]string{}, idStem: map[string]string{}}
	for _, a := range d.Actions {
		act := srAction{isList: a.Annotations.Properties.IsList, isWrite: a.Annotations.Properties.IsWrite}
		for _, r := range a.Resources {
			act.resources = append(act.resources, r.Name)
		}
		s.actions[a.Name] = act
		// Tagging-only writes (CreateTags) do not make "tag" a resource noun.
		if act.isWrite && !a.Annotations.Properties.IsTaggingOnly {
			_, noun := splitVerb(a.Name)
			s.writeNoun[sdkinv.Ident(noun)] = true
		}
	}
	for _, o := range d.Operations {
		s.opAction[o.Name] = pickAction(d.Name, o.Name, o.AuthorizedActions)
	}
	for _, r := range d.Resources {
		var vars []string
		for _, f := range r.ARNFormats {
			var v []string
			for _, m := range arnVarRe.FindAllStringSubmatch(f, -1) {
				switch m[1] {
				case "Partition", "Region", "Account":
				default:
					v = append(v, m[1])
				}
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
		}
		if len(vars) > 0 {
			if stem := memberStem(vars[len(vars)-1]); stem != "" {
				s.idStem[stem] = r.Name
			}
		}
	}
	return s
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
		switch {
		case sigv4.Name != "":
			return sigv4.Name, svc.SDKID
		case svc.ArnNamespace != "":
			return svc.ArnNamespace, svc.SDKID
		default:
			return svc.EndpointPrefix, svc.SDKID
		}
	}
	return "", ""
}

func indexModel(entries map[string]*entry, m *smithyModel, srAll map[string]*srService, file string) (string, []sdkinv.Operation) {
	svc, _ := serviceKey(m)
	if svc == "" {
		return "no service shape with a signing name", nil
	}
	var other []sdkinv.Operation
	sr := srAll[svc]
	diag := ""
	if sr == nil {
		diag = fmt.Sprintf("service %q absent from the Service Reference; classification falls back to SDK shape", svc)
	}
	module := fmt.Sprintf("aws-sdk-go-v2@%s/%s%s", sdkinv.AWSSDKRef, smithyModelsDir, file)
	for id, sh := range m.Shapes {
		if sh.Type != "operation" {
			continue
		}
		op := id[strings.LastIndex(id, "#")+1:]
		o := analyzeOp(m, sh)
		verb, noun := splitVerb(op)
		nounCanon := sdkinv.Ident(noun)
		signals := map[string]bool{}
		act, hasAct := srAction{}, false
		if sr != nil {
			act, hasAct = sr.actions[sr.opAction[op]]
			if !hasAct {
				act, hasAct = sr.actions[op]
			}
		}
		// The catalog's IsList is incomplete (backup-gateway:ListGateways,
		// batch:DescribeComputeEnvironments carry false): a read verb over a
		// collection output is a lister whatever the annotation says.
		shapeList := listVerbs[verb] && o.listMembers >= 1
		var isList bool
		switch {
		case hasAct:
			isList = act.isList || (shapeList && !act.isWrite)
			signals["sr:action"] = true
			if !act.isList && isList {
				signals["shape-list"] = true
			}
		default:
			isList = shapeList
			signals["fallback"] = true
		}
		if !isList && (o.listMembers != 0 || !detailVerbs[verb]) {
			// Writes, actions, batch reads: never a candidate.
			other = append(other, sdkinv.Operation{Service: svc, Name: op, Label: svc + ":" + op, Required: o.required, Scope: sdkinv.ScopeAccount, Module: module})
			continue
		}
		lin := lineage(sr, act, hasAct, o, nounCanon, signals)
		class := classify(isList, o, sr, nounCanon, lin, signals)
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
		en.class = sdkinv.StrongerClass(en.class, class)
		for _, r := range refsOf(m, sh, nounCanon) {
			en.refs[r] = true
		}
		for s := range signals {
			en.signals[s] = true
		}
		en.ops = append(en.ops, sdkinv.Operation{
			Service: svc, Name: op, Label: svc + ":" + op, IsList: isList, Paged: o.paged,
			Required: o.required, Targets: lin.targets, Scope: sdkinv.ScopeAccount, Module: module,
		})
	}
	return diag, other
}

// place is where an operation's subject sits in the resource tree.
// place folds one op's lineage into the entry. Several ops land on one key
// (ListResolvers, ListResolversByFunction); the shallowest lineage wins, ties
// by parent identity then by its display, so the pick does not follow the
// model's map order.
func (en *entry) place(lin place) {
	parent := ""
	if lin.depth > 0 && lin.parent != "" {
		parent = lin.parent
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
		switch {
		case selfStems[stem] || stem == nounCanon:
			lin.self = true
		case sr != nil && sr.idStem[stem] != "":
			lin.targets = append(lin.targets, stem)
			disp[stem] = sdkinv.Canon(sr.idStem[stem])
			lin.depth = max(lin.depth, len(sr.resources[sr.idStem[stem]]))
		case idLikeRe.MatchString(r):
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

// memberStem is the identity of the noun a member or ARN variable names:
// "KeyId" → Ident("key"), "Name" → "". memberDisp is its display form.
func memberStem(name string) string { return sdkinv.Ident(idLikeRe.ReplaceAllString(name, "")) }
func memberDisp(name string) string { return sdkinv.Canon(idLikeRe.ReplaceAllString(name, "")) }

type opShape struct {
	required    []string
	paged       bool
	listMembers int  // output members that are collections (structs, or id-named primitives like TableNames)
	hasIDs      bool // some collection element carries an id-like member
}

func analyzeOp(m *smithyModel, sh *shape) opShape {
	var o opShape
	_, o.paged = sh.Traits["smithy.api#paginated"]
	if sh.Input != nil {
		if in := m.Shapes[sh.Input.Target]; in != nil {
			for name, mem := range in.Members {
				if _, req := mem.Traits["smithy.api#required"]; req && !scopeParams[name] {
					o.required = append(o.required, name)
				}
			}
			sort.Strings(o.required)
		}
	}
	if sh.Output != nil {
		if out := m.Shapes[sh.Output.Target]; out != nil {
			for name, mem := range out.Members {
				t := m.Shapes[mem.Target]
				if t == nil || t.Type != "list" || t.Member == nil {
					continue
				}
				el := m.Shapes[t.Member.Target]
				switch {
				case el != nil && el.Type == "structure":
					o.listMembers++
					for f := range el.Members {
						if idLikeRe.MatchString(f) {
							o.hasIDs = true
						}
					}
				case idLikeRe.MatchString(name):
					o.listMembers++
					o.hasIDs = true
				}
			}
		}
	}
	return o
}

func classify(isList bool, o opShape, sr *srService, nounCanon string, lin place, signals map[string]bool) sdkinv.Class {
	if !isList {
		signals["detail-read"] = true
		return sdkinv.ClassAttribute
	}
	if len(lin.targets) >= crossCuttingTargets {
		signals["cross-cutting"] = true
		return sdkinv.ClassAttribute
	}
	if sr != nil {
		if name, ok := sr.resCanon[nounCanon]; ok {
			signals["sr:resource="+name] = true
			return sdkinv.ClassResource
		}
	}
	if lin.depth > 0 {
		if !o.hasIDs {
			signals["id-less-collection"] = true
			return sdkinv.ClassAttribute
		}
		signals["child-uncatalogued"] = true
		return sdkinv.ClassResource
	}
	if sr != nil && sr.writeNoun[nounCanon] {
		signals["writable-noun"] = true
		return sdkinv.ClassResource
	}
	if o.listMembers == 0 {
		signals["no-collection"] = true
		return sdkinv.ClassNonResource
	}
	signals["read-only"] = true
	return sdkinv.ClassCatalog
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
		c := sdkinv.Candidate{Provider: "aws", Service: en.service, Key: key, Depth: en.depth, Class: en.class, Ops: en.ops}
		if en.depth > 0 && en.parentID != "" {
			c.Parent = en.service + "/" + parentDisplay(entries, en)
		}
		for s := range en.signals {
			c.Signals = append(c.Signals, s)
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

// shorter orders display forms: fewer characters first, then lexically.
func shorter(a, b string) bool { return len(a) < len(b) || (len(a) == len(b) && a < b) }

func parentDisplay(entries map[string]*entry, en *entry) string {
	if p := entries[en.service+"/"+en.parentID]; p != nil {
		return p.display()
	}
	return en.parentDisp
}
