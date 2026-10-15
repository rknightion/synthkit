# synthkit composition root

Lease-mode operational providers own the standard-log bridge for their current role. Detach the
bridge before provider shutdown, restore the previous writer and preserve buffered event resources.
A late duplicate teardown must not detach a newer provider. Disabled self-observability leaves the
standard logger unchanged. The real local OTLP receiver in the HA operational integration test must
observe standard-log records, not only direct structured EmitEvent calls.
