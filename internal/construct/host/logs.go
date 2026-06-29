// SPDX-License-Identifier: AGPL-3.0-only

// logs.go — host log lane builder. Produces Loki streams for the four host log
// source types: Linux systemd-journal, Windows event log, macOS unified log (file),
// and Docker container stdout/stderr.
//
// Stream-label contracts (from real Alloy integration configs — Appendix B of the plan):
//
//	Linux journal:
//	  Stream labels:  {job:integrations/node_exporter, instance, unit, level}
//	                  (+ boot_id, transport — low-card OK)
//	  Line.Meta:      uid, pid, command, executable, syslog_identifier
//
//	Windows event:
//	  Stream labels:  {job:integrations/windows_exporter, instance, level, source, agent_hostname}
//	  Line.Meta:      event_id, provider
//
//	macOS file:
//	  Stream labels:  {job:integrations/macos-node, instance, hostname, sender}
//	  Line.Meta:      (pid dropped from labels per stage.label_drop in Alloy config)
//	  Body format:    sender[pid]: message
//
//	Docker:
//	  Stream labels:  {job:integrations/docker, instance, container, stream}
//	  Line.Meta:      container_id (optional)
//
// HIGH-CARD INVARIANT: pid, uid, container_id, boot_id (when long UUID form), command,
// executable MUST NOT be stream labels — they ride in Line.Meta. The loki sink asserts
// this; violations panic.
package host

import (
	"fmt"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// buildLogs constructs the host log streams for one tick at `now`. Returns nil
// (no-op) when h.Logs is false. The shape engine is used for per-tick variation;
// host-stable choices (unit, container name, stream direction) are derived from
// hashUnit keyed by seed strings for determinism (I12 — no global rand).
func buildLogs(h *fixture.Host, seed string, now time.Time, eng *shape.Engine) []loki.Stream {
	if !h.Logs {
		return nil
	}

	var out []loki.Stream

	switch h.OS {
	case "windows":
		out = append(out, buildWindowsEventStreams(h, now, eng)...)
	case "darwin", "macos":
		out = append(out, buildMacOSStreams(h, now, eng)...)
	default: // linux (default)
		out = append(out, buildLinuxJournalStreams(h, now, eng)...)
	}

	// Docker container log lane — emitted regardless of OS (Linux only in practice,
	// but the gate is h.Docker as set by the resolver; macOS docker is rejected there).
	if h.Docker {
		out = append(out, buildDockerStreams(h, seed, now, eng)...)
	}

	return out
}

// ─── Linux journal ────────────────────────────────────────────────────────────

// linuxUnits are realistic systemd unit names used as the `unit` stream label.
// Low cardinality: these appear on every production Linux host. Sourced from the
// homelab reference node_exporter journal streams.
var linuxUnits = []string{
	"systemd-journald.service",
	"sshd.service",
	"cron.service",
	"kernel",
	"NetworkManager.service",
	"systemd-resolved.service",
	"systemd-networkd.service",
	"systemd-udevd.service",
	"systemd-logind.service",
	"containerd.service",
	"docker.service",
	"alloy.service",
	"tailscaled.service",
	"rsyslog.service",
}

// linuxLevels are journal priority keywords (info weighted heavily, warn/err rare).
var linuxLevels = []string{"info", "info", "info", "info", "warning", "error"}

// linuxUnitCommand maps a systemd unit to its real journal `_COMM` (the process
// binary name) stamped as the `command` structured-metadata field. Values are taken
// VERBATIM from homelab reference node_exporter journal streams (
// host-a/host-b/host-c, captured 2026-06-18). Note _COMM is kernel-truncated to 15 chars,
// which is why e.g. systemd-resolved.service => "systemd-resolve" (no trailing 'd') and
// systemd-networkd.service => "systemd-network" — these are the real captured values.
// A unit absent from this map falls back to its base name (see commandForUnit).
var linuxUnitCommand = map[string]string{
	"systemd-journald.service": "systemd-journal", // _COMM truncated at 15
	"sshd.service":             "sshd-session",    // modern OpenSSH per-connection binary
	"ssh.service":              "sshd-session",    // Debian/Ubuntu unit name variant
	"cron.service":             "cron",
	"crond.service":            "crond",  // RHEL/Fedora variant
	"kernel":                   "kernel", // no _COMM upstream; syslog_identifier value
	"NetworkManager.service":   "NetworkManager",
	"systemd-resolved.service": "systemd-resolve", // _COMM truncated at 15
	"systemd-networkd.service": "systemd-network", // _COMM truncated at 15
	"systemd-udevd.service":    "(udev-worker)",   // real captured _COMM
	"systemd-logind.service":   "systemd-logind",
	"containerd.service":       "containerd",
	"docker.service":           "dockerd",
	"alloy.service":            "alloy",
	"tailscaled.service":       "tailscaled",
	"rsyslog.service":          "rsyslogd",
	"qemu-guest-agent.service": "qemu-ga",
	"avahi-daemon.service":     "avahi-daemon",
	"nscd.service":             "nscd",
	"postfix.service":          "pickup",
}

// commandForUnit returns the real journal `_COMM` for a systemd unit, grounded in
// captured homelab reference data. For a unit not in linuxUnitCommand, it falls back to the unit's
// base name (strip a trailing ".service"). Deterministic; no global rand (I12).
func commandForUnit(unit string) string {
	if c, ok := linuxUnitCommand[unit]; ok {
		return c
	}
	return strings.TrimSuffix(unit, ".service")
}

// linuxSyslogIDs mirrors realistic syslog_identifier values (one per unit class),
// captured from homelab reference journal streams. Differs from _COMM in several real cases
// (e.g. cron.service => command "cron" but syslog_identifier "CRON").
var linuxSyslogIDs = map[string]string{
	"sshd.service":             "sshd-session",
	"ssh.service":              "sshd-session",
	"cron.service":             "CRON",
	"crond.service":            "CROND",
	"kernel":                   "kernel",
	"NetworkManager.service":   "NetworkManager",
	"systemd-resolved.service": "systemd-resolved",
	"systemd-networkd.service": "systemd-networkd",
	"systemd-udevd.service":    "(udev-worker)",
	"systemd-logind.service":   "systemd-logind",
	"containerd.service":       "containerd",
	"docker.service":           "dockerd",
	"alloy.service":            "alloy",
	"tailscaled.service":       "tailscaled",
	"rsyslog.service":          "rsyslogd",
	"systemd-journald.service": "systemd-journald",
}

// linuxMessages are representative journal body lines, indexed by level.
var linuxMessages = map[string][]string{
	"info": {
		"Started %s.",
		"Reached target %s.",
		"Listening on %s Socket.",
		"New session 42 of user root.",
		"Accepted publickey for deploy from 10.0.0.1 port 54321 ssh2: RSA SHA256:abc123",
		"Job succeeded for unit %s.",
		"Configuration successfully applied.",
		"System clock synchronized.",
		"Removed slice %s.",
		"Deactivated successfully.",
	},
	"warning": {
		"Service %s has run into watchdog timeout.",
		"Unit %s entered degraded state.",
		"Time has been changed",
		"Failed to send audit message: Operation not permitted",
	},
	"error": {
		"Failed to start %s.",
		"Unit %s failed with result 'exit-code'.",
		"Error receiving data on socket: Connection reset by peer",
	},
}

// buildLinuxJournalStreams produces one journal stream per selected unit, grouping
// log lines under {job, instance, unit, level}. Lines per tick: 10–20 total across
// all streams.
func buildLinuxJournalStreams(h *fixture.Host, now time.Time, eng *shape.Engine) []loki.Stream {
	// Stable per-host boot_id derived from hostname (low-card across ticks — same host
	// uses the same boot_id within a process lifetime; it IS low-card enough to be a
	// stream label, matching real Alloy behaviour).
	bootID := stableBootID(h.Hostname)

	// Select 3-5 units deterministically from the linuxUnits catalog using the hostname hash.
	unitCount := 3 + int(hashSpan(h.Hostname+":unitcount", 0, 2))
	selectedUnits := make([]string, 0, unitCount)
	for i := 0; i < unitCount; i++ {
		idx := int(hashSpan(fmt.Sprintf("%s:unit:%d", h.Hostname, i), 0, int64(len(linuxUnits)-1)))
		selectedUnits = append(selectedUnits, linuxUnits[idx])
	}

	// Target ~10-20 lines total; distribute across units, round-robin.
	linesPerTick := 10 + int(eng.Float64()*11)
	linesPerUnit := max1(linesPerTick / len(selectedUnits))

	var streams []loki.Stream
	for _, unit := range selectedUnits {
		// Level is per-unit-and-tick: weighted toward info.
		levelIdx := int(hashUnit(fmt.Sprintf("%s:%s:%d", h.Hostname, unit, now.Unix()/60))) % len(linuxLevels)
		// Add a small per-tick float influence so level can shift tick-to-tick.
		levelIdx = (levelIdx + int(eng.Float64()*float64(len(linuxLevels)))) % len(linuxLevels)
		level := linuxLevels[levelIdx]

		syslogID := linuxSyslogIDs[unit]
		if syslogID == "" {
			syslogID = unit
		}
		// `command` (journal _COMM) is derived from the UNIT, grounded in homelab reference
		// captures — NOT picked by an unrelated unit index (was a correlation bug).
		command := commandForUnit(unit)
		executable := "/usr/bin/" + command

		// Stable per-host-per-unit PID (low-variation; PIDs in structured metadata, not labels).
		pid := 1000 + int(hashSpan(fmt.Sprintf("%s:%s:pid", h.Hostname, unit), 1, 65534))

		lines := make([]loki.Line, 0, linesPerUnit)
		msgs := linuxMessages[level]
		if len(msgs) == 0 {
			msgs = linuxMessages["info"]
		}
		for j := 0; j < linesPerUnit; j++ {
			msgIdx := (int(hashUnit(fmt.Sprintf("%s:%s:msg:%d:%d", h.Hostname, unit, now.Unix(), j)))) % len(msgs)
			body := fmt.Sprintf(msgs[msgIdx], unit)
			// Spread the log lines evenly across the 60s tick window.
			lineT := now.Add(-time.Duration(linesPerUnit-j) * time.Second)
			lines = append(lines, loki.Line{
				T:    lineT,
				Body: body,
				Meta: map[string]string{
					"pid":               fmt.Sprintf("%d", pid+j),
					"uid":               "0",
					"command":           command,
					"executable":        executable,
					"syslog_identifier": syslogID,
				},
			})
		}

		streams = append(streams, loki.Stream{
			Labels: map[string]string{
				"job":       "integrations/node_exporter",
				"instance":  h.Hostname,
				"unit":      unit,
				"level":     level,
				"boot_id":   bootID,
				"transport": "journal",
			},
			Lines: lines,
		})
	}
	return streams
}

// stableBootID returns a stable low-cardinality boot identifier for a host,
// formatted as an 8-char hex string (low-card: same host = same boot_id across ticks).
// NOTE: This is intentionally NOT a full UUID — UUIDs are high-card (Loki sink asserts).
// The real Alloy journal module uses the full boot_id UUID as a stream label; however,
// since that violates the loki sink's high-cardinality assertion in this implementation,
// we use a short stable hex form. See Appendix B: boot_id is listed as low-card OK.
func stableBootID(hostname string) string {
	v := hashUnit(hostname + ":boot_id")
	return fmt.Sprintf("%08x", uint32(v*0xffffffff))
}

// ─── Windows event log ────────────────────────────────────────────────────────

// windowsEventSources is the set of event sources emitted per channel.
var windowsEventSources = map[string][]string{
	"Application": {
		"Application Error",
		"Application Hang",
		"Service Control Manager",
		"Windows Error Reporting",
		"Microsoft-Windows-Security-SPP",
	},
	"System": {
		"Microsoft-Windows-Kernel-General",
		"Microsoft-Windows-Kernel-Power",
		"Microsoft-Windows-EventLog",
		"Microsoft-Windows-Winlogon",
		"Tcpip",
		"Disk",
	},
}

// windowsLevels maps event level strings (as emitted by Alloy's loki.source.windowsevent).
var windowsLevels = []string{"Information", "Information", "Information", "Warning", "Error"}

// windowsMessages provides representative event log lines per channel.
var windowsMessages = map[string][]string{
	"Application": {
		"The description for Event ID 1000 from source Application Error cannot be found.",
		"Service Control Manager: The %s service entered the running state.",
		"Service Control Manager: The %s service entered the stopped state.",
		"Application: Application started successfully.",
		"Windows Error Reporting: Fault bucket %d, type 5",
	},
	"System": {
		"The system time has changed to %s.",
		"The kernel-mode driver %s has been successfully loaded.",
		"Power Policy Manager: The effective power policy has changed to Balanced.",
		"Event Log Service started.",
		"Windows is starting up.",
		"The computer has rebooted from a bugcheck.",
	},
}

// buildWindowsEventStreams produces two streams: Application and System channels.
func buildWindowsEventStreams(h *fixture.Host, now time.Time, eng *shape.Engine) []loki.Stream {
	channels := []string{"Application", "System"}
	linesPerChannel := 3 + int(eng.Float64()*5)

	var streams []loki.Stream
	for _, ch := range channels {
		sources := windowsEventSources[ch]
		msgs := windowsMessages[ch]

		// Stable per-channel level (varies per host and tick bucket).
		levelIdx := int(hashUnit(fmt.Sprintf("%s:%s:%d", h.Hostname, ch, now.Unix()/300))) % len(windowsLevels)
		level := windowsLevels[levelIdx]

		lines := make([]loki.Line, 0, linesPerChannel)
		for j := 0; j < linesPerChannel; j++ {
			srcIdx := int(hashUnit(fmt.Sprintf("%s:%s:src:%d:%d", h.Hostname, ch, now.Unix(), j))) % len(sources)
			msgIdx := int(hashUnit(fmt.Sprintf("%s:%s:msg:%d:%d", h.Hostname, ch, now.Unix(), j))) % len(msgs)
			eventID := 1000 + int(hashSpan(fmt.Sprintf("%s:%s:eid:%d", h.Hostname, ch, j), 0, 999))
			body := fmt.Sprintf(msgs[msgIdx], sources[srcIdx])
			lineT := now.Add(-time.Duration(linesPerChannel-j) * time.Second)
			lines = append(lines, loki.Line{
				T:    lineT,
				Body: body,
				Meta: map[string]string{
					"event_id": fmt.Sprintf("%d", eventID),
					"provider": sources[srcIdx],
				},
			})
		}

		streams = append(streams, loki.Stream{
			Labels: map[string]string{
				"job":            "integrations/windows_exporter",
				"instance":       h.Hostname,
				"level":          level,
				"source":         ch,
				"agent_hostname": h.Hostname,
			},
			Lines: lines,
		})
	}
	return streams
}

// ─── macOS file log ───────────────────────────────────────────────────────────

// macOSSenders are realistic macOS log sender names (process/subsystem names).
// These match what appears in /var/log/*.log via the Unified Log export on macOS.
var macOSSenders = []string{
	"kernel",
	"syslogd",
	"com.apple.SecurityFramework",
	"com.apple.system.logger",
	"com.apple.xpc.launchd",
	"com.apple.CoreUtils",
	"com.apple.networkd",
	"loginwindow",
	"notifyd",
	"configd",
}

// macOSMessages provides representative macOS log lines.
var macOSMessages = []string{
	"System started.",
	"AppleHV: Successfully mapped memory region.",
	"Network interface en0 attached.",
	"Sandbox: %s denied network-inbound.",
	"com.apple.security: trust evaluation succeeded for policy %d.",
	"Process exited with code 0.",
	"Time changed to %s.",
	"Login: login succeeded for user console.",
	"Sleep requested.",
	"Wake requested.",
}

// buildMacOSStreams produces macOS unified log streams. Alloy's macos-node
// integration reads from /var/log/*.log. Stream labels: {job, instance, hostname, sender}.
// Body format: sender[pid]: message (pid NOT a stream label — per stage.label_drop in Alloy).
func buildMacOSStreams(h *fixture.Host, now time.Time, eng *shape.Engine) []loki.Stream {
	// Select 2-3 senders deterministically.
	senderCount := 2 + int(hashSpan(h.Hostname+":macossenders", 0, 1))
	linesPerSender := 3 + int(eng.Float64()*5)

	var streams []loki.Stream
	for i := 0; i < senderCount; i++ {
		idx := int(hashUnit(fmt.Sprintf("%s:macos:sender:%d", h.Hostname, i))) % len(macOSSenders)
		sender := macOSSenders[idx]

		// Stable synthetic PID for this sender (not a stream label — goes in body).
		pid := 100 + int(hashSpan(fmt.Sprintf("%s:macos:pid:%s", h.Hostname, sender), 1, 9999))

		lines := make([]loki.Line, 0, linesPerSender)
		for j := 0; j < linesPerSender; j++ {
			msgIdx := int(hashUnit(fmt.Sprintf("%s:macos:msg:%d:%d", h.Hostname, now.Unix(), j))) % len(macOSMessages)
			// Body in sender[pid]: message format (the Alloy stage.regex result).
			body := fmt.Sprintf("%s[%d]: %s", sender, pid+j, fmt.Sprintf(macOSMessages[msgIdx], sender))
			lineT := now.Add(-time.Duration(linesPerSender-j) * time.Second)
			lines = append(lines, loki.Line{
				T:    lineT,
				Body: body,
				// pid is NOT in Meta either — it is embedded in the body only,
				// matching the real Alloy stage.label_drop pid behaviour.
			})
		}

		streams = append(streams, loki.Stream{
			Labels: map[string]string{
				"job":      "integrations/macos-node",
				"instance": h.Hostname,
				"hostname": h.Hostname,
				"sender":   sender,
			},
			Lines: lines,
		})
	}
	return streams
}

// ─── Docker container logs ────────────────────────────────────────────────────

// dockerLogLines provides representative container stdout/stderr lines.
var dockerStdoutLines = []string{
	"[%s] INFO  Server listening on :8080",
	"[%s] INFO  Health check passed",
	"[%s] DEBUG Request processed in 3ms",
	"[%s] INFO  Cache hit ratio: 98%%",
	"[%s] INFO  Connected to database",
	"[%s] INFO  Worker ready",
}

var dockerStderrLines = []string{
	"[%s] WARN  Retrying connection attempt 1/3",
	"[%s] ERROR Failed to connect to upstream: connection refused",
	"[%s] WARN  High memory usage detected",
}

// buildDockerStreams produces one stdout + one stderr stream per container.
// Container list is derived from dockerContainers(seed) (topology.go) using the SAME
// per-construct seed (fx.Seed = "<blueprint>:host:<hostname>") the metric lane uses, so
// the docker LOG container set (names+ids) matches the docker METRIC container set.
func buildDockerStreams(h *fixture.Host, seed string, now time.Time, eng *shape.Engine) []loki.Stream {
	containers := dockerContainers(seed)

	var streams []loki.Stream
	for ci, ct := range containers {
		// The metric series carry name/image/id (native cadvisor labels); the LOGS lane
		// re-derives the container name from the `name` label and stamps it as the
		// `container` STREAM label (the Alloy docker log discovery relabel — correct here).
		containerName := ct.Labels["name"]
		if containerName == "" {
			continue
		}

		// Stdout stream: 2-5 lines per tick.
		stdoutCount := 2 + int(hashSpan(fmt.Sprintf("%s:docker:stdout:%d:%d", h.Hostname, ci, now.Unix()/60), 0, 3))
		stdoutLines := make([]loki.Line, 0, stdoutCount)
		for j := 0; j < stdoutCount; j++ {
			msgIdx := int(hashUnit(fmt.Sprintf("%s:docker:stdout:msg:%d:%d:%d", h.Hostname, ci, now.Unix(), j))) % len(dockerStdoutLines)
			body := fmt.Sprintf(dockerStdoutLines[msgIdx], containerName)
			lineT := now.Add(-time.Duration(stdoutCount-j) * time.Second)
			stdoutLines = append(stdoutLines, loki.Line{
				T:    lineT,
				Body: body,
			})
		}

		streams = append(streams, loki.Stream{
			Labels: map[string]string{
				"job":       "integrations/docker",
				"instance":  h.Hostname,
				"container": containerName,
				"stream":    "stdout",
			},
			Lines: stdoutLines,
		})

		// Stderr stream: 0-2 lines per tick (errors are rare).
		stderrCount := int(hashSpan(fmt.Sprintf("%s:docker:stderr:%d:%d", h.Hostname, ci, now.Unix()/300), 0, 2))
		if stderrCount == 0 {
			// Add at least 1 line so the stream is non-empty and testable.
			// Use a small per-container probability driven by the engine.
			if eng.Float64() < 0.5 {
				stderrCount = 1
			}
		}
		if stderrCount > 0 {
			stderrLines := make([]loki.Line, 0, stderrCount)
			for j := 0; j < stderrCount; j++ {
				msgIdx := int(hashUnit(fmt.Sprintf("%s:docker:stderr:msg:%d:%d:%d", h.Hostname, ci, now.Unix(), j))) % len(dockerStderrLines)
				body := fmt.Sprintf(dockerStderrLines[msgIdx], containerName)
				lineT := now.Add(-time.Duration(stderrCount-j) * time.Second)
				stderrLines = append(stderrLines, loki.Line{
					T:    lineT,
					Body: body,
				})
			}
			streams = append(streams, loki.Stream{
				Labels: map[string]string{
					"job":       "integrations/docker",
					"instance":  h.Hostname,
					"container": containerName,
					"stream":    "stderr",
				},
				Lines: stderrLines,
			})
		}
	}
	return streams
}

// max1 returns n when n >= 1, else 1. Used to prevent zero lines-per-unit.
func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
