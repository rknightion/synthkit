# Chat Scenarios

`synthkit-chat` adds people to a failure story. When an operator activates a SynthKit
[scenario](incidents.md), a small cast of simulated coworkers talks in Slack around the real alert
it raises: someone notices the slowdown, the incident commander asks for an ack, the backend owner
is pulled in when asked. The operator is the only human in the room and drives every Grafana IRM
button himself. Personas ask him to act; they never claim an IRM action.

It is offline tooling in its own binary. It never links into the `synthkit` emit binary, and it
talks to SynthKit only by polling `GET /control/state`.

## Three files

| File | Holds | Committed? |
|---|---|---|
| Scenario (`*.chat.yaml`) | cast, logical channels, facts, beats, guardrails, model choice | the generic example is; your own stories stay private |
| Bindings (`bindings.yaml`) | Slack channel ids, the operator's user id, IRM's bot or app id, the control-plane URL, and the *names* of the secret variables | never |
| Environment | `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN`, `OPENROUTER_API_KEY`, `CONTROL_TOKEN` (names configurable in bindings) | never |

The repository ships a generic example triggered by the bundled
`acme-ai-platform/backend_latency_storm` scenario, a Slack app manifest and a bindings template,
all under `examples/chat/`.

## Rehearse in the terminal

```bash
go run ./cmd/synthkit-chat console -scenario examples/chat/backend-latency.chat.yaml
```

Console mode needs no Slack, no SynthKit and, with the default `-llm fake`, no model. You are the
operator: plain lines are your messages in the current channel. Type `start` (or `:synthkit on`,
which honours `trigger.delay`), then inject what the platform would do:

| Input | Effect |
|---|---|
| `:irm alert` | an IRM alert-group post in the current channel |
| `:irm ack`, `:irm resolve` | the alert post edited to acknowledged or resolved |
| `:channel incident-42` | a new channel is created; a scenario channel with `bind` takes it |
| `:in incident-42` | speak in another channel |
| `:synthkit off` | the SynthKit scenario is deactivated (the wind-down beat runs) |
| `status`, `pause`, `resume`, `stop`, `skip <beat>`, `say <persona> <text>` | the kill switch |

Add `-llm openrouter` (with `OPENROUTER_API_KEY` set) to rehearse against the real model, and
`-trigger manual` to start at once. Every run writes a JSONL journal under `chatsim-runs/`.

## Run it live

1. Create a Slack app from `examples/chat/slack-app-manifest.yaml`
   ("From a manifest" at api.slack.com), install it, copy the bot token, and create an
   app-level token with `connections:write`. Socket Mode needs no public URL.
2. Invite the app to the static channels. New public channels whose name matches a scenario
   channel's `bind.name_match` are joined automatically and their recent history is read; a
   private channel needs the app invited by hand.
3. Copy `examples/chat/bindings.example.yaml` somewhere private and fill in the ids.
4. Capture IRM's identity: `synthkit-chat tap -bindings b.yaml -out tap.jsonl -join-match '^incident-'`,
   fire a test alert, and read IRM's `bot_id` off the captured post. `tap` posts nothing. Use the
   same capture to write `when.slack.text_match` regexes against the real payloads.
5. Check everything offline: `synthkit-chat validate -scenario story.chat.yaml -bindings b.yaml`.
6. Run: `synthkit-chat run -scenario story.chat.yaml -bindings b.yaml`. The run arms when the
   configured `trigger.synthkit_scenario` goes from inactive to active in `/control/state`
   (HTTP Basic `control:<token>`); a scenario that is already active at launch never fires.
   `-trigger manual` starts immediately instead.

The kill switch is `/chatsim start | pause | resume | stop | status | skip <beat> | say <persona> <text>`,
accepted only from `operator_user_id` and answered privately. The same commands work on the
terminal's stdin.

## Writing a scenario

```yaml
apiVersion: synthkit.chatsim/v1
kind: ChatScenario
name: my-drill
seed: 7
trigger: { synthkit_scenario: acme-ai-platform/backend_latency_storm, delay: 20s }
llm: { model: deepseek/deepseek-v4.1-flash-1017, zdr: false }
operator: { name: Casey, role: on-call SRE }
channels:
  ops: {}
  incident: { bind: { on: channel_created, name_match: "^incident-" }, fallback: ops }
personas:
  - { id: alex, name: Alex Moreno, role: Incident commander, style: short and calm,
      avatar: { emoji: ":large_blue_circle:" }, present: [ops, incident] }
beats:
  - { id: alert_lands, when: { slack: { from: irm, text_match: "(?i)firing" } },
      speakers: [alex], goal: Alex asks Casey to acknowledge the alert, record: { fact: alert_fired, from: event } }
```

Decoding is strict: an unknown field is an error. Each beat has exactly one trigger:

| Trigger | Fires |
|---|---|
| `at: +30s` | that long after chat t0 (activation plus `trigger.delay`) |
| `after: other_beat+90s` | that long after another beat opened |
| `when.operator.match` | on an operator message matching the regex |
| `when.slack` | on a message (`from: irm \| operator \| other`), `message_changed`, `channel_created` or `member_joined_channel`, with `text_match` over the flattened text, blocks and attachments included |
| `when.synthkit: activated \| deactivated` | on the SynthKit scenario edge |
| `when.summoned: <persona>` | when a summoned persona arrives |

A beat opens, gets up to `max_turns` lines from its `speakers`, and closes when its turns are used,
when the director marks it done, or at `expires_after`. `then: [{ summon: ben, channel: incident, after: 25s..45s }]`
brings a persona in after the beat's first line: a narrator line in the app's own identity
("... was added to the channel by ...") is posted just before the persona's first message. A
`final: true` beat ends the run once it is done; `end.after` bounds every run.

Personas post with `chat.postMessage` plus a per-message username and `icon_emoji`. They are never
edited and never scheduled on Slack's side. Avatars are emoji only.

## What keeps it honest

One director call per wake-up returns up to `max_lines_per_turn` lines as strict JSON
(`response_format` `json_schema`, `strict: true`, with OpenRouter `provider.require_parameters`
and `data_collection: deny`; `zdr` is a knob). The engine validates the answer locally and retries
once, then applies deterministic guardrails before anything is posted:

- lines only for an open beat, a persona the operator addressed by name, or the single idle nudge;
- `max_turns`, per-minute caps (global and per persona), `max_llm_calls` and `end.after`;
- a quiet period after each operator message for everyone he did not address;
- **no IRM claims**: a persona saying it acknowledged, resolved, escalated, silenced, paged or
  declared anything is rejected outright;
- a completed operational action ("restarted", "rolled back") must cite a ledger entry or a beat
  `may_claim`; a number with a unit or a URL must already be in the ledger;
- no `@here`, `@channel`, app or user mentions other than the operator, no slash commands, no
  names outside the cast, no out-of-character AI talk, and `max_line_chars`.

A rejected line is never posted. Typing delays scale with line length and `typing_wpm`, and each
channel is paced to one post per second.

## Current limits

Operator intent classification, run resume and reconciliation, threads and reactions, image
avatars and concurrent runs are not implemented yet.
