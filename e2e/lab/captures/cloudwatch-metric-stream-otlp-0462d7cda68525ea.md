# CloudWatch Metric Streams OTLP gateway probe — 2026-09-05

Payload SHA-256: `0462d7cda68525ea07f9f0174b3fe7670b81f9cce50aedf04d99a5448e762b5d`. One root-owned push; no gateway configuration change, restart, CR edit or pipeline edit. Synthetic identifiers in the payload are fixture identities.

## CloudWatch Metric Streams OTLP — observed 2026-09-05 [slug: cw-metric-stream-otlp]

The root sent one OTLP/HTTP request through an existing gateway receiver, then queried the metrics backend after more than 90 seconds. Receipt was HTTP 200 with `{"partialSuccess":{}}`. Read-back returned 13 probe series: 12 Summary components plus one `target_info`. This proves queryability for the tested EC2 and RDS forms, not every AWS namespace.

Wire names retain `amazonaws.com/{Namespace}/{MetricName}`. The datapoint is Summary: count=SampleCount, sum=Sum, quantiles 0 and 1 carry minimum and maximum. Average is computed as sum/count; the five-stat remote-write expansion is not the OTLP representation. Units come from AWS's UCUM table: Percent `%`, Count `{Count}`. Resource attributes are `cloud.provider`, `cloud.account.id`, `cloud.region`; the scratch probe additionally used `service.name` to isolate read-back. An empty Dimensions map is omitted.

Observed queryable names:

```text
amazonaws_com_AWS_EC2_CPUUtilization_percent
amazonaws_com_AWS_EC2_CPUUtilization_percent_count
amazonaws_com_AWS_EC2_CPUUtilization_percent_sum
amazonaws_com_AWS_RDS_DatabaseConnections
amazonaws_com_AWS_RDS_DatabaseConnections_count
amazonaws_com_AWS_RDS_DatabaseConnections_sum
target_info
```

Dots and slashes normalized to underscores; case survived. Percent gained `_percent`; `{Count}` added no unit suffix. Each Summary produced `_count`, `_sum`, and two quantile series (labels `quantile="0"` and `quantile="1"`). No `_average`, `_maximum`, `_minimum`, `_sample_count`, or `_ratio` series appeared.

`Namespace` and `MetricName` became labels with case preserved. Nested `Dimensions` was serialized into ONE JSON-valued `Dimensions` label; it was not flattened into `Dimensions.*` labels. The account aggregate had no `Dimensions` label. `cloud.region` was promoted as `cloud_region`; `service.name` as `service_name` and `job`. `cloud.account.id` and `cloud.provider` appeared only on `target_info` in this observation. No scope labels appeared.

Provenance: AWS CloudWatch OpenTelemetry 1.0.0 format and translation documentation, retrieved through Context7 with the exact translation table checked at its official source; collector-contrib `awscloudwatchreceiver` README. Capture: [`cloudwatch-metric-stream-otlp-0462d7cda68525ea.md](cloudwatch-metric-stream-otlp-0462d7cda68525ea.md). The wire spelling must never be replaced with the post-gateway spelling in emitters.

## Exact payload sent

```json
{
  "resourceMetrics": [
    {
      "resource": {
        "attributes": [
          {
            "key": "service.name",
            "value": {
              "stringValue": "synthkit-sk89-probe"
            }
          },
          {
            "key": "cloud.provider",
            "value": {
              "stringValue": "aws"
            }
          },
          {
            "key": "cloud.region",
            "value": {
              "stringValue": "eu-west-1"
            }
          },
          {
            "key": "cloud.account.id",
            "value": {
              "stringValue": "411000000099"
            }
          }
        ]
      },
      "scopeMetrics": [
        {
          "scope": {},
          "metrics": [
            {
              "name": "amazonaws.com/AWS/EC2/CPUUtilization",
              "unit": "%",
              "summary": {
                "dataPoints": [
                  {
                    "attributes": [
                      {
                        "key": "Namespace",
                        "value": {
                          "stringValue": "AWS/EC2"
                        }
                      },
                      {
                        "key": "MetricName",
                        "value": {
                          "stringValue": "CPUUtilization"
                        }
                      },
                      {
                        "key": "Dimensions",
                        "value": {
                          "kvlistValue": {
                            "values": [
                              {
                                "key": "InstanceId",
                                "value": {
                                  "stringValue": "i-0a11ce00000000001"
                                }
                              }
                            ]
                          }
                        }
                      }
                    ],
                    "startTimeUnixNano": "1788623405753837000",
                    "timeUnixNano": "1788623465753837000",
                    "count": "60",
                    "sum": 2137.2,
                    "quantileValues": [
                      {
                        "quantile": 0,
                        "value": 31.7
                      },
                      {
                        "quantile": 1,
                        "value": 41.9
                      }
                    ]
                  },
                  {
                    "attributes": [
                      {
                        "key": "Namespace",
                        "value": {
                          "stringValue": "AWS/EC2"
                        }
                      },
                      {
                        "key": "MetricName",
                        "value": {
                          "stringValue": "CPUUtilization"
                        }
                      }
                    ],
                    "startTimeUnixNano": "1788623405753837000",
                    "timeUnixNano": "1788623465753837000",
                    "count": "120",
                    "sum": 4382.4,
                    "quantileValues": [
                      {
                        "quantile": 0,
                        "value": 30.8
                      },
                      {
                        "quantile": 1,
                        "value": 44.6
                      }
                    ]
                  }
                ]
              }
            },
            {
              "name": "amazonaws.com/AWS/RDS/DatabaseConnections",
              "unit": "{Count}",
              "summary": {
                "dataPoints": [
                  {
                    "attributes": [
                      {
                        "key": "Namespace",
                        "value": {
                          "stringValue": "AWS/RDS"
                        }
                      },
                      {
                        "key": "MetricName",
                        "value": {
                          "stringValue": "DatabaseConnections"
                        }
                      },
                      {
                        "key": "Dimensions",
                        "value": {
                          "kvlistValue": {
                            "values": [
                              {
                                "key": "DBInstanceIdentifier",
                                "value": {
                                  "stringValue": "synthkit-sk89-database"
                                }
                              }
                            ]
                          }
                        }
                      }
                    ],
                    "startTimeUnixNano": "1788623405753837000",
                    "timeUnixNano": "1788623465753837000",
                    "count": "60",
                    "sum": 2460,
                    "quantileValues": [
                      {
                        "quantile": 0,
                        "value": 37
                      },
                      {
                        "quantile": 1,
                        "value": 47
                      }
                    ]
                  }
                ]
              }
            }
          ]
        }
      ]
    }
  ]
}
```

## Gateway response

```text
HTTP 200
{"partialSuccess":{}}
```

## Authenticated read-back

### probe-series

```json
{
  "status": "success",
  "data": [
    {
      "Dimensions": "{\"DBInstanceIdentifier\":\"synthkit-sk89-database\"}",
      "MetricName": "DatabaseConnections",
      "Namespace": "AWS/RDS",
      "__name__": "amazonaws_com_AWS_RDS_DatabaseConnections",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "0",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"DBInstanceIdentifier\":\"synthkit-sk89-database\"}",
      "MetricName": "DatabaseConnections",
      "Namespace": "AWS/RDS",
      "__name__": "amazonaws_com_AWS_RDS_DatabaseConnections",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "1",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"DBInstanceIdentifier\":\"synthkit-sk89-database\"}",
      "MetricName": "DatabaseConnections",
      "Namespace": "AWS/RDS",
      "__name__": "amazonaws_com_AWS_RDS_DatabaseConnections_count",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"DBInstanceIdentifier\":\"synthkit-sk89-database\"}",
      "MetricName": "DatabaseConnections",
      "Namespace": "AWS/RDS",
      "__name__": "amazonaws_com_AWS_RDS_DatabaseConnections_sum",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"InstanceId\":\"i-0a11ce00000000001\"}",
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "0",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"InstanceId\":\"i-0a11ce00000000001\"}",
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "1",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"InstanceId\":\"i-0a11ce00000000001\"}",
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent_count",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"InstanceId\":\"i-0a11ce00000000001\"}",
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent_sum",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "0",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "1",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent_count",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent_sum",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "__name__": "target_info",
      "cloud_account_id": "411000000099",
      "cloud_provider": "aws",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    }
  ],
  "warnings": [
    "results may be truncated due to querier.max-series-query-limit (requested limit: 0, enforced: 100000)"
  ]
}

```

### probe-labels

```json
{
  "status": "success",
  "data": [
    "Dimensions",
    "MetricName",
    "Namespace",
    "__name__",
    "cloud_account_id",
    "cloud_provider",
    "cloud_region",
    "job",
    "quantile",
    "service_name"
  ]
}

```

### target-info

```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {
        "metric": {
          "__name__": "target_info",
          "cloud_account_id": "411000000099",
          "cloud_provider": "aws",
          "cloud_region": "eu-west-1",
          "job": "synthkit-sk89-probe",
          "service_name": "synthkit-sk89-probe"
        },
        "value": [
          1788623605.461,
          "1"
        ]
      }
    ]
  }
}

```


## Independent amazonaws-name selector read-back

```json
{
  "status": "success",
  "data": [
    {
      "Dimensions": "{\"DBInstanceIdentifier\":\"synthkit-sk89-database\"}",
      "MetricName": "DatabaseConnections",
      "Namespace": "AWS/RDS",
      "__name__": "amazonaws_com_AWS_RDS_DatabaseConnections",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "0",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"DBInstanceIdentifier\":\"synthkit-sk89-database\"}",
      "MetricName": "DatabaseConnections",
      "Namespace": "AWS/RDS",
      "__name__": "amazonaws_com_AWS_RDS_DatabaseConnections",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "1",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"DBInstanceIdentifier\":\"synthkit-sk89-database\"}",
      "MetricName": "DatabaseConnections",
      "Namespace": "AWS/RDS",
      "__name__": "amazonaws_com_AWS_RDS_DatabaseConnections_count",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"DBInstanceIdentifier\":\"synthkit-sk89-database\"}",
      "MetricName": "DatabaseConnections",
      "Namespace": "AWS/RDS",
      "__name__": "amazonaws_com_AWS_RDS_DatabaseConnections_sum",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"InstanceId\":\"i-0a11ce00000000001\"}",
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "0",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"InstanceId\":\"i-0a11ce00000000001\"}",
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "1",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"InstanceId\":\"i-0a11ce00000000001\"}",
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent_count",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "Dimensions": "{\"InstanceId\":\"i-0a11ce00000000001\"}",
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent_sum",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "0",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "quantile": "1",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent_count",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    },
    {
      "MetricName": "CPUUtilization",
      "Namespace": "AWS/EC2",
      "__name__": "amazonaws_com_AWS_EC2_CPUUtilization_percent_sum",
      "cloud_region": "eu-west-1",
      "job": "synthkit-sk89-probe",
      "service_name": "synthkit-sk89-probe"
    }
  ],
  "warnings": [
    "results may be truncated due to querier.max-series-query-limit (requested limit: 0, enforced: 100000)"
  ]
}

```
