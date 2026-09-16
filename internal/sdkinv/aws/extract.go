package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	writeNoun map[string]bool     // CanonSingular(noun of an IsWrite action) -> true
	resCanon  map[string]string   // CanonSingular(resource name) -> resource name
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
	selfStems   = map[string]bool{"": true, "resource": true, "target": true}
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
	service string
	noun    string
	depth   int
	parent  string
	class   sdkinv.Class
	signals map[string]bool
	ops     []sdkinv.Operation
}

func (extractor) Extract(_ context.Context, dir string) (*sdkinv.Universe, error) {
	u := &sdkinv.Universe{Provider: "aws", Pins: map[string]string{"aws-sdk-go-v2": sdkinv.AWSSDKRef}}
	sr, srVersion, err := loadServiceReference(filepath.Join(dir, "service-reference"))
	if err != nil {
		return nil, err
	}
	u.Pins["service-reference"] = srVersion
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
		if diag := indexModel(entries, &m, sr, filepath.Base(f)); diag != "" {
			u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "warn", Source: filepath.Base(f), Message: diag})
		}
	}
	mergeDetailReads(entries)
	for key, en := range entries {
		u.Candidates = append(u.Candidates, toCandidate(key, en))
	}
	sdkinv.SortCandidates(u.Candidates)
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
	st, err := os.Stat(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, "", err
	}
	return out, st.ModTime().UTC().Format("2006-01-02"), nil
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
			s.writeNoun[sdkinv.CanonSingular(noun)] = true
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
			s.resCanon[sdkinv.CanonSingular(r.Name)] = r.Name
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

func indexModel(entries map[string]*entry, m *smithyModel, srAll map[string]*srService, file string) string {
	svc, _ := serviceKey(m)
	if svc == "" {
		return "no service shape with a signing name"
	}
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
		nounCanon := sdkinv.CanonSingular(noun)
		signals := map[string]bool{}
		act, hasAct := srAction{}, false
		if sr != nil {
			act, hasAct = sr.actions[sr.opAction[op]]
			if !hasAct {
				act, hasAct = sr.actions[op]
			}
		}
		var isList bool
		if hasAct {
			isList = act.isList
			signals["sr:action"] = true
		} else {
			isList = listVerbs[verb] && o.listMembers == 1
			signals["fallback"] = true
		}
		if !isList && (o.listMembers != 0 || !detailVerbs[verb]) {
			continue // writes, actions, batch reads: never a candidate
		}
		lin := lineage(sr, act, hasAct, o, nounCanon, signals)
		class := classify(isList, o, sr, nounCanon, lin, signals)
		key := svc + "/" + nounCanon
		if class == sdkinv.ClassAttribute && lin.parent != "" && !lin.self {
			key = svc + "/" + lin.parent + "/" + nounCanon
		}
		en := entries[key]
		if en == nil {
			en = &entry{service: svc, noun: noun, depth: -1, signals: map[string]bool{}}
			entries[key] = en
		}
		if en.depth < 0 || lin.depth < en.depth {
			en.depth = lin.depth
			en.parent = ""
			if lin.depth > 0 && lin.parent != "" {
				en.parent = svc + "/" + lin.parent
			}
		}
		en.class = sdkinv.StrongerClass(en.class, class)
		for s := range signals {
			en.signals[s] = true
		}
		en.ops = append(en.ops, sdkinv.Operation{
			Service: svc, Name: op, Label: svc + ":" + op, IsList: isList, Paged: o.paged,
			Required: o.required, Targets: lin.targets, Scope: sdkinv.ScopeAccount, Module: module,
		})
	}
	return diag
}

// place is where an operation's subject sits in the resource tree.
type place struct {
	targets []string // catalog resources the operation is authorised against
	self    bool     // one target is the subject itself
	depth   int
	parent  string // CanonSingular parent noun, "" when unknown
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
			if sdkinv.CanonSingular(r) == nounCanon {
				lin.self = true
				lin.depth = len(vars) - 1
				if len(vars) > 1 {
					lin.parent = memberStem(vars[len(vars)-2])
				}
				break
			}
			parents = append(parents, r)
		}
		if !lin.self && len(parents) > 0 {
			p := pickParent(sr, parents, o.required)
			lin.parent = sdkinv.CanonSingular(p)
			lin.depth = len(sr.resources[p])
		}
		return lin
	}
	for _, r := range o.required {
		stem := memberStem(r)
		switch {
		case selfStems[stem] || stem == nounCanon:
			lin.self = true
		case sr != nil && sr.idStem[stem] != "":
			lin.targets = append(lin.targets, stem)
			lin.depth = max(lin.depth, len(sr.resources[sr.idStem[stem]]))
		case idLikeRe.MatchString(r):
			lin.targets = append(lin.targets, stem)
			lin.depth = max(lin.depth, 1)
		}
	}
	sort.Strings(lin.targets)
	if len(lin.targets) > 0 {
		lin.parent = lin.targets[len(lin.targets)-1]
		signals["required-id"] = true
	}
	return lin
}

// memberStem is the noun a member or ARN variable names: "KeyId" → "key",
// "BucketName" → "bucket", "Name" → "".
func memberStem(name string) string {
	return sdkinv.CanonSingular(idLikeRe.ReplaceAllString(name, ""))
}

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
		tc := sdkinv.CanonSingular(t)
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

func toCandidate(key string, en *entry) sdkinv.Candidate {
	c := sdkinv.Candidate{Provider: "aws", Service: en.service, Key: key, Depth: en.depth, Class: en.class, Parent: en.parent, Ops: en.ops}
	for s := range en.signals {
		c.Signals = append(c.Signals, s)
	}
	sort.Strings(c.Signals)
	return c
}
