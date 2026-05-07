package disco

import rego.v1

# AWS Well-Architected Framework — Operational Excellence pillar, OPS 8:
# Use observability to understand workload health. Single-region CloudTrail
# trails miss API calls in every other region — gaps in the audit log
# directly limit incident response and compliance defensibility.
#
# CloudTrail scanner wraps the SDK Trail under attrs.Trail and the trail
# status under attrs.Status; read IsMultiRegionTrail off the wrapped Trail.

deny contains f if {
	input.type == "aws:cloudtrail:trail"
	input.attributes.Trail.IsMultiRegionTrail == false
	f := {
		"id": "waf-ops-cloudtrail-not-multi-region",
		"severity": "medium",
		"category": "aws-waf",
		"message": sprintf("CloudTrail %q is single-region; API calls in other regions are not logged", [input.name]),
		"remediation": "Reconfigure as a multi-region trail or add per-region trails covering the full footprint.",
		"ref_url": "https://docs.aws.amazon.com/wellarchitected/latest/operational-excellence-pillar/ops_observability_understand_workload_health.html",
		"tags": {
			"waf_pillar": "operations",
			"waf_qid": "OPS 8",
			"soc2": "CC7.2",
			"iso27001": "A.8.15",
			"pci_dss": "10.2",
			"nist_800_53": "AU-2",
		},
	}
}
