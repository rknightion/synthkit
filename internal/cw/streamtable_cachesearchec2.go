// SPDX-License-Identifier: AGPL-3.0-only

package cw

// AWS reference pages used for the entries below:
//   - AWS/EC2: Amazon EC2 User Guide, "View available metrics",
//     https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/viewing_metrics_with_cloudwatch.html
//
// The name was checked through Context7 on 2026-09-05. An entry remains absent until its exact
// AWS name, namespace, unit, and dimensions are verified.
func streamTableCacheSearchEC2() streamTable {
	return streamTable{
		entries: map[string]StreamEntry{
			"aws_ec2_cpuutilization": {Namespace: "AWS/EC2", MetricName: "CPUUtilization", Unit: "%"},
		},
		dimensions: map[string]map[string]string{
			"aws_ec2_cpuutilization": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
		},
	}
}
