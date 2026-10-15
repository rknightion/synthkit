# synthkit composition root

Lease-mode operational providers own the standard-log bridge for their current role. Detach the
bridge before provider shutdown, restore the previous writer and preserve buffered event resources.
A late duplicate teardown must not detach a newer provider. Disabled self-observability leaves the
standard logger unchanged. The real local OTLP receiver in the HA operational integration test must
observe standard-log records, not only direct structured EmitEvent calls.

The optional one-shot deadline belongs to validation/preflight only. Check explicit RFC3339 input
and expired entry before configuration loading, carry the absolute context through endpoint probes
and recheck before success. An omitted deadline preserves the normal modes. A canceled request does
not roll back receiver-side processing; supervisors must use the command exit status.
