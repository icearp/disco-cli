package disco

import rego.v1

# AWS Well-Architected Framework — Cost Optimization pillar, COST 4:
# Decommission resources. EBS volumes in the "available" state carry no
# attachments — every billing cycle pays gigabyte-month for storage no
# workload reads.

deny contains f if {
	input.type == "aws:ec2:volume"
	input.attributes.State == "available"
	f := {
		"id": "waf-cost-ebs-volume-unattached",
		"severity": "low",
		"category": "aws-waf",
		"message": sprintf("EBS volume %q is unattached (state=available); accruing storage charges", [input.nativeId]),
		"remediation": "Snapshot if needed for forensics, then delete; or attach to a running instance.",
		"refUrl": "https://docs.aws.amazon.com/wellarchitected/latest/cost-optimization-pillar/cost_decomission_resources.html",
		"tags": {
			"waf_pillar": "cost",
			"waf_qid": "COST 4",
		},
	}
}
