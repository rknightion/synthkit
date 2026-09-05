// SPDX-License-Identifier: AGPL-3.0-only

package cw

// AWS reference pages used for the entries below:
//   - AWS/RDS: Amazon RDS User Guide, "Monitoring Amazon RDS metrics with Amazon CloudWatch",
//     https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/monitoring-cloudwatch.html
//
// The name was checked through Context7 on 2026-09-05. An entry remains absent until its exact
// AWS name, namespace, unit, and dimensions are verified.
func streamTableRDSFamily() streamTable {
	return streamTable{
		entries: map[string]StreamEntry{
			"aws_rds_database_connections": {Namespace: "AWS/RDS", MetricName: "DatabaseConnections", Unit: "{Count}"},
		},
		dimensions: map[string]map[string]string{
			"aws_rds_database_connections": {
				"dimension_DBInstanceIdentifier": "DBInstanceIdentifier",
			},
		},
	}
}
