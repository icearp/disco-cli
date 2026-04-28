package aws

import (
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	eventstypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// Helpers that build resolver-test AttributesJSON from real SDK structs so
// any drift in scanner-side wrapping shape (added/removed wrapper key,
// renamed field, type change in SDK upgrade) surfaces here rather than as
// silent zero-value resolutions in production.
//
// Each helper mirrors a wrapper container declared inline in the matching
// scanner file. The wrapper structs there are unexported function-local
// types, so they cannot be reused directly — these helpers reproduce the
// shape with anonymous structs that share the same json tags.

// elbv2LBAttrs reproduces the wrapping shape produced by elb_scanners.go:
// {"lb": <SDK LoadBalancer>, "type": "<lb-type>"}.
func elbv2LBAttrs(lb elbv2types.LoadBalancer) string {
	return mustJSON(map[string]any{"lb": lb, "type": string(lb.Type)})
}

// elbv2TargetGroupAttrs reproduces tgWithTargets in elb_scanners.go:
// {"TargetGroup": <SDK TargetGroup>, "Targets": [<SDK TargetDescription>...]}.
func elbv2TargetGroupAttrs(tg elbv2types.TargetGroup, targets ...elbv2types.TargetDescription) string {
	return mustJSON(struct {
		TargetGroup elbv2types.TargetGroup         `json:"TargetGroup"`
		Targets     []elbv2types.TargetDescription `json:"Targets"`
	}{TargetGroup: tg, Targets: targets})
}

// eventBridgeRuleAttrs reproduces ruleWithTargets in eventbridge_scanners.go:
// {"Rule": <SDK Rule>, "Targets": [<SDK Target>...]}.
func eventBridgeRuleAttrs(rule eventstypes.Rule, targets ...eventstypes.Target) string {
	return mustJSON(struct {
		Rule    eventstypes.Rule     `json:"Rule"`
		Targets []eventstypes.Target `json:"Targets"`
	}{Rule: rule, Targets: targets})
}
