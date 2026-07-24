// aws-resolver-audit walks every AWS resource's AttributesJSON, extracts ARN
// and bare-ID references to other scanned types, then diffs the proposed
// (source_type, target_type) pairs against edges already persisted in
// `relationships`. Audit-but-not-actual pairs surface as candidate resolver
// gaps, ranked by sample frequency.
//
// Run against a populated DB:
//
//	go run ./cmd/aws-resolver-audit --db ~/.local/share/disco/disco.db
//	go run ./cmd/aws-resolver-audit --db ~/.local/share/disco/disco.db --top 50
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/internal/coverage"
	awsprov "github.com/icearp/disco-cli/internal/providers/aws"
	"github.com/icearp/disco-cli/store"
)

func main() {
	if code := run(); code != 0 {
		os.Exit(code)
	}
}

type pairKey struct{ src, tgt string }

type gap struct {
	src, tgt string
	topPath  string
	freq     int
}

func run() int {
	dbPath := flag.String("db", "", "path to disco.db (required unless --list-edges)")
	top := flag.Int("top", 0, "limit output to top N gap pairs (0 = all)")
	sample := flag.Int("sample", 5, "rows sampled per type")
	listEdges := flag.Bool("list-edges", false, "dump every (source, target, kind) edge declared via EdgeDecl metadata, then exit")
	flag.Parse()
	if *listEdges {
		fmt.Println("source_type\ttarget_type\tkind")
		for _, e := range awsprov.CollectResolverEdges() {
			fmt.Printf("%s\t%s\t%s\n", e.Source, e.Target, e.Kind)
		}
		return 0
	}
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "--db required")
		return 2
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		return 1
	}
	defer st.Close()

	resources, err := st.ListResources(store.ResourceFilter{
		Providers:      []string{"aws"},
		IncludeManaged: true,
		Limit:          1 << 30,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "list resources: %v\n", err)
		return 1
	}
	byType := groupByType(resources, *sample)
	knownTypes := loadKnownTypes(byType)
	hits := collectHits(byType, knownTypes)
	edges, err := st.ListRelationships()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list relationships: %v\n", err)
		return 1
	}
	actualBoth := buildCoverSet(resources, edges)
	gaps := rankGaps(hits, actualBoth)
	printGaps(gaps, byType, *sample, *top)
	return 0
}

func groupByType(resources []store.Resource, sample int) map[string][]store.Resource {
	byType := map[string][]store.Resource{}
	for _, r := range resources {
		if len(byType[r.Type]) < sample {
			byType[r.Type] = append(byType[r.Type], r)
		}
	}
	return byType
}

// loadKnownTypes builds the full disco-type catalog from the AWS coverage
// Provider — every Type* const ever declared, regardless of scan state.
// Falls back to scanned types when the catalog can't load.
func loadKnownTypes(byType map[string][]store.Resource) map[string]struct{} {
	knownTypes := make(map[string]struct{})
	if p, ok := coverage.Get("aws"); ok {
		for _, decl := range p.Emits() {
			knownTypes[decl.DiscoType] = struct{}{}
		}
	}
	if len(knownTypes) == 0 {
		for t := range byType {
			knownTypes[t] = struct{}{}
		}
	}
	return knownTypes
}

// collectHits walks each sampled row's AttributesJSON, classifies every
// ARN/bare-ID reference, and accumulates (source, target) → field-path
// hit counts. Self-NativeID, unknown-target, and same-type refs are skipped.
func collectHits(byType map[string][]store.Resource, knownTypes map[string]struct{}) map[pairKey]map[string]int {
	hits := map[pairKey]map[string]int{}
	for src, rows := range byType {
		for _, r := range rows {
			for _, ref := range extractRefs(r.AttributesJSON) {
				// Skip self-NativeID — e.g. SLR's `Arn` matches `:role/`
				// and classifies as TypeIAMRole (the SLR row is itself
				// aws:iam:service-linked-role): same physical resource,
				// no implementable cross-edge.
				if ref.value == r.NativeID {
					continue
				}
				tgt := classifyTarget(ref.value)
				if tgt == "" {
					continue
				}
				if _, ok := knownTypes[tgt]; !ok {
					continue
				}
				if tgt == src {
					continue // self-edge, not useful
				}
				k := pairKey{src, tgt}
				if hits[k] == nil {
					hits[k] = map[string]int{}
				}
				hits[k][ref.path]++
			}
		}
	}
	return hits
}

// buildCoverSet groups DB-emitted edges + EdgeDecl metadata into a
// direction-blind {source, target} set used to mark hits as covered.
func buildCoverSet(resources []store.Resource, edges []store.Relationship) map[pairKey]struct{} {
	idType := make(map[string]string, len(resources))
	for _, r := range resources {
		idType[r.ID] = r.Type
	}
	out := map[pairKey]struct{}{}
	for _, e := range edges {
		s, ok1 := idType[e.FromID]
		t, ok2 := idType[e.ToID]
		if !ok1 || !ok2 {
			continue
		}
		out[pairKey{s, t}] = struct{}{}
		out[pairKey{t, s}] = struct{}{}
	}
	for _, decl := range awsprov.CollectResolverEdges() {
		out[pairKey{decl.Source, decl.Target}] = struct{}{}
		out[pairKey{decl.Target, decl.Source}] = struct{}{}
	}
	return out
}

func rankGaps(hits map[pairKey]map[string]int, covered map[pairKey]struct{}) []gap {
	var gaps []gap
	for pk, paths := range hits {
		if _, ok := covered[pk]; ok {
			continue
		}
		var topPath string
		topFreq := 0
		total := 0
		for p, c := range paths {
			total += c
			if c > topFreq {
				topFreq = c
				topPath = p
			}
		}
		gaps = append(gaps, gap{pk.src, pk.tgt, topPath, total})
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].freq != gaps[j].freq {
			return gaps[i].freq > gaps[j].freq
		}
		if gaps[i].src != gaps[j].src {
			return gaps[i].src < gaps[j].src
		}
		return gaps[i].tgt < gaps[j].tgt
	})
	return gaps
}

func printGaps(gaps []gap, byType map[string][]store.Resource, sample, top int) {
	fmt.Printf("# AWS resolver gaps — %d candidate pairs\n", len(gaps))
	fmt.Printf("# scanned types: %d, sampled rows: %d\n", len(byType), sample)
	fmt.Printf("# note column: target_unscanned = no rows of target type in DB,\n")
	fmt.Printf("#              so a resolver that emits FK-safely produces zero\n")
	fmt.Printf("#              edges and stays indistinguishable from no resolver.\n\n")
	fmt.Println("source_type\ttarget_type\tfreq\ttop_field_path\tnote")
	limit := len(gaps)
	if top > 0 && top < limit {
		limit = top
	}
	for _, g := range gaps[:limit] {
		note := ""
		if _, scanned := byType[g.tgt]; !scanned {
			note = "target_unscanned"
		}
		fmt.Printf("%s\t%s\t%d\t%s\t%s\n", g.src, g.tgt, g.freq, g.topPath, note)
	}
}

// ref is one (path, value) string found inside a resource's AttributesJSON.
type ref struct {
	path  string
	value string
}

// extractRefs walks a JSON blob and returns every leaf string value alongside
// its dotted JSON path. Path uses `[i]` for arrays.
func extractRefs(blob string) []ref {
	if blob == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(blob), &v); err != nil {
		return nil
	}
	var out []ref
	walk("", v, &out)
	return out
}

func walk(path string, v any, out *[]ref) {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			p := k
			if path != "" {
				p = path + "." + k
			}
			walk(p, vv, out)
		}
	case []any:
		for i, vv := range x {
			p := fmt.Sprintf("%s[%d]", path, i)
			walk(p, vv, out)
		}
	case string:
		if looksLikeRef(x) {
			*out = append(*out, ref{path: path, value: x})
		}
	}
}

// looksLikeRef returns true when the string carries an ARN prefix or matches
// a well-known AWS resource-ID prefix.
func looksLikeRef(s string) bool {
	if strings.HasPrefix(s, "arn:") {
		return true
	}
	for _, p := range idPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// idPrefixes — bare resource-ID prefixes worth classifying. Used by
// looksLikeRef as a presence test, so order does not affect correctness here
// (classifyBareID is the order-sensitive consumer).
var idPrefixes = []string{
	"vpc-", "subnet-", "sg-", "eni-", "ami-", "i-", "vol-", "snap-",
	"igw-", "nat-", "rtb-", "pl-", "eipalloc-", "tgw-", "vgw-", "cgw-",
	"dx-", "dxcon-", "dxvif-", "dxgw-", "vpce-", "acl-", "fpga-",
	"lt-", "key-", "fs-", "fsmt-", "fsap-",
}

// arnRE captures the service segment from a canonical AWS ARN.
//
//	arn:{partition}:{service}:{region}:{account}:{resource}
var arnRE = regexp.MustCompile(`^arn:aws[a-z-]*:([a-z0-9-]+):([a-z0-9-]*):(\d*):(.+)$`)

// classifyTarget maps a reference string to a candidate disco type. Returns
// "" when the reference doesn't map to any known scanned type.
func classifyTarget(ref string) string {
	if strings.HasPrefix(ref, "arn:") {
		m := arnRE.FindStringSubmatch(ref)
		if m == nil {
			return ""
		}
		svc := m[1]
		resource := m[4]
		return classifyARN(svc, resource)
	}
	return classifyBareID(ref)
}

// classifyARN maps (service, resource-segment) to a disco type best-effort.
// Resource segment is everything after account; first segment before `/` or
// `:` is usually the resource kind.
func classifyARN(svc, resource string) string {
	kind := resource
	if i := strings.IndexAny(resource, "/:"); i > 0 {
		kind = resource[:i]
	}
	// Service-segment normalisation: AWS uses both pluralised
	// (`elasticfilesystem`) and abbreviated (`logs`, `s3`) forms.
	svc = arnServiceAlias(svc)
	if svc == "" {
		return ""
	}
	// Map kind segment → disco type suffix.
	suffix := arnKindSuffix(svc, kind)
	if suffix == "" {
		return ""
	}
	return "aws:" + svc + ":" + suffix
}

// arnServiceAlias normalises AWS ARN service segments to disco service
// segments (e.g. `elasticfilesystem` → `efs`). Empty result = unmapped.
func arnServiceAlias(s string) string {
	switch s {
	case "elasticfilesystem":
		return "efs"
	case "elasticloadbalancing":
		return "elb"
	case "elasticbeanstalk":
		return "elasticbeanstalk"
	case "states":
		return "stepfunctions"
	case "execute-api", "apigateway":
		return "apigateway"
	case "ecr-public":
		return "ecrpublic"
	case "cognito-idp":
		return "cognito-idp"
	case "cognito-identity":
		return "cognito-identity"
	case "elasticmapreduce":
		return "emr"
	case "monitoring":
		return "cloudwatch"
	}
	return s
}

// arnKindSuffix maps an ARN kind segment to a disco-type suffix per service.
// Conservative: returns "" when uncertain so we don't manufacture phantom
// gap rows. Service-aware mappings can be added by branching on `svc` first;
// today the mappings are generic enough to ignore it.
func arnKindSuffix(_ /*svc*/, kind string) string {
	// Generic identity suffix mappings shared across services.
	generic := map[string]string{
		"key":               "key",
		"alias":             "alias",
		"function":          "function",
		"layer":             "layer",
		"role":              "role",
		"user":              "user",
		"group":             "group",
		"policy":            "policy",
		"instance-profile":  "instance-profile",
		"vpc":               "vpc",
		"subnet":            "subnet",
		"security-group":    "security-group",
		"network-interface": "network-interface",
		"volume":            "volume",
		"snapshot":          "snapshot",
		"image":             "image",
		"instance":          "instance",
		"key-pair":          "key-pair",
		"loadbalancer":      "load-balancer",
		"targetgroup":       "target-group",
		"listener":          "listener",
		"log-group":         "log-group",
		"secret":            "secret",
		"parameter":         "parameter",
		"topic":             "topic",
		"queue":             "queue",
		"table":             "table",
		"stream":            "stream",
		"bucket":            "bucket",
		"cluster":           "cluster",
		"service":           "service",
		"task-definition":   "task-definition",
		"db":                "instance", // RDS db: → instance
		"cluster-snapshot":  "cluster-snapshot",
		"file-system":       "file-system",
		"event-bus":         "event-bus",
		"rule":              "rule",
		"pipeline":          "pipeline",
		"stateMachine":      "state-machine",
		"activity":          "activity",
	}
	if s, ok := generic[kind]; ok {
		return s
	}
	return ""
}

// bareIDPrefixMap is consulted by classifyBareID. Prefix order matters:
// longer/more-specific prefixes must precede shorter shared prefixes
// (e.g. `igw-` before `i-`, `tgw-attach-` before `tgw-`).
var bareIDPrefixMap = []struct {
	prefix, discoType string
}{
	{"tgw-attach-", "aws:ec2:transit-gateway-attachment"},
	{"tgw-rtb-", "aws:ec2:transit-gateway-route-table"},
	{"tgw-mc-", "aws:ec2:transit-gateway-multicast-domain"},
	{"eipalloc-", "aws:ec2:elastic-ip"},
	{"subnet-", "aws:ec2:subnet"},
	{"snap-", "aws:ec2:snapshot"},
	{"fpga-", "aws:ec2:fpga-image"},
	{"vpce-", "aws:ec2:vpc-endpoint"},
	{"dxgw-", "aws:directconnect:gateway"},
	{"dxcon-", "aws:directconnect:connection"},
	{"dxvif-", "aws:directconnect:virtual-interface"},
	{"fsmt-", "aws:efs:mount-target"},
	{"fsap-", "aws:efs:access-point"},
	{"vpc-", "aws:ec2:vpc"},
	{"sg-", "aws:ec2:security-group"},
	{"eni-", "aws:ec2:network-interface"},
	{"ami-", "aws:ec2:image"},
	{"igw-", "aws:ec2:internet-gateway"},
	{"vol-", "aws:ec2:volume"},
	{"nat-", "aws:ec2:nat-gateway"},
	{"rtb-", "aws:ec2:route-table"},
	{"pl-", "aws:ec2:prefix-list"},
	{"tgw-", "aws:ec2:transit-gateway"},
	{"vgw-", "aws:ec2:vpn-gateway"},
	{"cgw-", "aws:ec2:customer-gateway"},
	{"dx-", "aws:directconnect:connection"},
	{"acl-", "aws:ec2:network-acl"},
	{"lt-", "aws:ec2:launch-template"},
	{"fs-", "aws:efs:file-system"},
	{"i-", "aws:ec2:instance"},
}

// classifyBareID maps a bare AWS ID (e.g. `vpc-abc123`) to a disco type.
func classifyBareID(id string) string {
	for _, p := range bareIDPrefixMap {
		if strings.HasPrefix(id, p.prefix) {
			return p.discoType
		}
	}
	return ""
}
