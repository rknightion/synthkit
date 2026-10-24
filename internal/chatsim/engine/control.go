// SPDX-License-Identifier: AGPL-3.0-only

package engine

import "strings"

// ControlVerbs are the kill-switch commands.
var ControlVerbs = []string{"start", "pause", "resume", "stop", "status", "skip", "say", "help"}

// ParseControl recognises "/chatsim <verb> [args]" or a bare "<verb> [args]". skip and say need
// arguments; the other verbs take none, so ordinary chat such as "stop the pods" is not a command.
func ParseControl(line string) (verb, args string, ok bool) {
	s := strings.TrimSpace(line)
	s = strings.TrimSpace(strings.TrimPrefix(s, "/chatsim"))
	verb, args, _ = strings.Cut(s, " ")
	verb, args = strings.ToLower(verb), strings.TrimSpace(args)
	for _, v := range ControlVerbs {
		if verb != v {
			continue
		}
		if v == "help" || (v == "skip" || v == "say") == (args != "") {
			return verb, args, true
		}
	}
	return "", "", false
}
