package disco

import rego.v1

# AWS Well-Architected Framework — Reliability pillar, REL 13: Plan for
# disaster recovery. RDS instances without Multi-AZ have no automated failover
# — a single AZ outage causes user-visible downtime until a manual restore
# from snapshot completes.

deny contains f if {
	input.type == "aws:rds:db-instance"
	input.attributes.MultiAZ == false
	f := {
		"id": "waf-rel-rds-multi-az-disabled",
		"severity": "medium",
		"category": "aws-waf",
		"message": sprintf("RDS instance %q is single-AZ (no automated failover)", [input.name]),
		"remediation": "Enable Multi-AZ; the next maintenance window provisions a standby in a second AZ.",
		"refUrl": "https://docs.aws.amazon.com/wellarchitected/latest/reliability-pillar/rel_planning_for_recovery_disaster_recovery.html",
		"tags": {
			"waf_pillar": "reliability",
			"waf_qid": "REL 13",
		},
	}
}
