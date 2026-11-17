// SPDX-License-Identifier: AGPL-3.0-only

package engine

import "strings"

// ControlVerbs are the kill-switch and control-DM commands.
var ControlVerbs = []string{"start", "pause", "resume", "stop", "status", "fire", "heal", "skip", "say", "summon", "direct", "reset", "help"}

// argVerbs need arguments; the other verbs take none, so ordinary chat such as "stop the pods"
// is not a command.
var argVerbs = map[string]bool{"skip": true, "say": true, "summon": true, "direct": true}

// ControlHelp lists the verbs for a help reply.
const ControlHelp = "start | fire | heal | pause | resume | stop | status | skip <beat> | say <persona> <text> | summon <persona> [channel] | direct <stage direction> | reset"

// ParseControl recognises "/chatsim <verb> [args]" or a bare "<verb> [args]".
func ParseControl(line string) (verb, args string, ok bool) {
	s := strings.TrimSpace(line)
	s = strings.TrimSpace(strings.TrimPrefix(s, "/chatsim"))
	verb, args, _ = strings.Cut(s, " ")
	verb, args = strings.ToLower(strings.TrimRight(verb, ".!?")), strings.TrimSpace(args)
	for _, v := range ControlVerbs {
		if verb != v {
			continue
		}
		if v == "help" || argVerbs[v] == (args != "") {
			return verb, args, true
		}
	}
	return "", "", false
}

// ParseDM reads one message to the control DM: a control verb, or else a stage direction for
// the cast.
func ParseDM(text string) (verb, args string) {
	if v, a, ok := ParseControl(text); ok {
		return v, a
	}
	return "direct", strings.TrimSpace(text)
}
