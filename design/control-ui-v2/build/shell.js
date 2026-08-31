// Shared markup helpers for the synthkit v2 screen files.
// Every value here is a design-system token; no hex literals except the canvas paper.
var MONO = 'font-family:var(--font-family-mono)';
var LBL = MONO + ';font-size:10px;text-transform:uppercase;letter-spacing:0.13em;color:var(--color-fg-muted)';
var LBLP = MONO + ';font-size:10px;text-transform:uppercase;letter-spacing:0.13em;color:oklch(0.45 0.018 227)';

function icon(name, size, extra) {
  size = size || 15;
  return '<svg width="' + size + '" height="' + size + '" fill="currentColor" style="flex:none' + (extra ? ';' + extra : '') + '"><use href="#i-' + name + '"></use></svg>';
}

function sectionLabel(text, meta, mt) {
  return '<div style="display:flex;align-items:baseline;gap:8px;' + (mt ? 'margin:' + mt + 'px 0 10px' : 'margin:0 0 10px') + '">' +
    '<span style="' + LBL + '">' + text + '</span>' +
    (meta ? '<span style="' + MONO + ';font-size:11px;color:var(--color-fg-muted)">' + meta + '</span>' : '') +
    '</div>';
}

function btn(kind, label, iconName, opts) {
  opts = opts || {};
  var base = 'height:' + (opts.h || 28) + 'px;display:inline-flex;align-items:center;gap:6px;padding:0 ' + (opts.px || 12) + 'px;border-radius:var(--radius-control);font-family:var(--font-family-sans);font-size:12.5px;font-weight:600;cursor:pointer;white-space:nowrap;';
  var skin = {
    primary: 'background:var(--color-bg-accent);color:var(--color-fg-on-accent);border:1px solid var(--color-bg-accent)',
    outline: 'background:transparent;color:var(--color-fg-default);border:1px solid var(--color-border-strong)',
    ghost: 'background:transparent;color:var(--color-fg-soft);border:1px solid transparent',
    destructive: 'background:color-mix(in oklab, var(--color-status-fail) 12%, transparent);color:var(--color-status-fail);border:1px solid color-mix(in oklab, var(--color-status-fail) 35%, transparent)',
    disabled: 'background:transparent;color:var(--color-fg-faint);border:1px solid var(--color-border-default);cursor:default'
  }[kind];
  return '<button type="button"' + (opts.attrs || '') + ' style="' + base + skin + (opts.extra ? ';' + opts.extra : '') + '">' +
    (iconName ? icon(iconName, opts.iconSize || 14) : '') + (label ? '<span>' + label + '</span>' : '') + '</button>';
}

function iconBtn(name, title) {
  return '<button type="button" title="' + title + '" style="width:28px;height:28px;display:inline-flex;align-items:center;justify-content:center;border:1px solid var(--color-border-strong);border-radius:var(--radius-control);background:transparent;color:var(--color-fg-soft);cursor:pointer">' + icon(name, 15) + '</button>';
}

function statusWord(kind, note) {
  var map = {
    ok: ['&#9632; OK', 'var(--color-status-ok)'],
    warn: ['&#9670; WARN', 'var(--color-status-warn)'],
    fail: ['&#9679; FAIL', 'var(--color-status-fail)'],
    live: ['&#9679; LIVE', 'var(--color-status-fail)'],
    mod: ['&#9670; MOD', 'var(--color-status-warn)'],
    off: ['&#9632; OFF', 'var(--color-fg-muted)'],
    idle: ['&#9632; IDLE', 'var(--color-fg-muted)'],
    queued: ['&#9632; QUEUED', 'var(--color-fg-muted)'],
    done: ['&#9632; DONE', 'var(--color-fg-muted)'],
    skipped: ['&#9670; SKIPPED', 'var(--color-status-warn)']
  }[kind];
  return '<span style="display:inline-flex;align-items:baseline;gap:8px">' +
    '<span style="' + MONO + ';font-size:11.5px;font-weight:600;letter-spacing:0.04em;color:' + map[1] + ';flex:none">' + map[0] + '</span>' +
    (note ? '<span style="font-size:11px;color:var(--color-fg-muted)">' + note + '</span>' : '') + '</span>';
}

// Inline magnitude meter: ink-derived track, zero renders as a dim 0 with no track.
function meter(value, max, band, label) {
  if (!value) return '<span style="' + MONO + ';font-size:12.5px;color:var(--color-fg-muted);font-variant-numeric:tabular-nums">0</span>';
  var pct = Math.min(100, Math.round((value / max) * 100));
  var shown = label || value;
  var col = band === 'fail' ? 'var(--color-status-fail)' : band === 'warn' ? 'var(--color-status-warn)' : 'var(--color-bg-accent)';
  return '<span style="display:inline-flex;align-items:center;justify-content:flex-end;gap:8px">' +
    '<span style="width:52px;height:4px;overflow:hidden;background:color-mix(in oklab, var(--color-fg-default) 14%, var(--color-bg-surface))">' +
    '<span style="display:block;height:100%;width:' + pct + '%;background:' + col + '"></span></span>' +
    '<span style="' + MONO + ';font-size:12.5px;font-weight:600;font-variant-numeric:tabular-nums;color:' + (band ? col : 'var(--color-fg-default)') + '">' + shown + '</span></span>';
}

function spark(points, w, h, colour) {
  w = w || 52; h = h || 14;
  return '<svg width="' + w + '" height="' + h + '" viewBox="0 0 ' + w + ' ' + h + '" fill="none" stroke="' + (colour || 'currentColor') + '" stroke-width="1.2" stroke-linejoin="round" stroke-linecap="round" style="display:block"><polyline points="' + points + '"></polyline></svg>';
}

var SPARK_A = '0,10 6,9 13,11 19,6 26,7 32,5 39,6 45,3 52,4';
var SPARK_B = '0,4 6,5 13,4 19,7 26,6 32,9 39,8 45,11 52,12';
var SPARK_FLAT = '0,7 6,7 13,6 19,7 26,7 32,7 39,6 45,7 52,7';

// ── sidebar ────────────────────────────────────────────────────────────────
var NAV = [
  ['Views', [
    ['overview', 'Overview', 'squares-four', '6'],
    ['config', 'Config', 'gear', '142'],
    ['health', 'Health', 'pulse', '86'],
    ['xray', 'X-ray', 'crosshair', '48.2k']
  ]],
  ['Global', [
    ['global', 'Global controls', 'faders', ''],
    ['schema', 'Blueprint schema', 'book-open', '214']
  ]],
  ['Chaos', [
    ['incidents', 'Incidents', 'lightning', 'live2']
  ]],
  ['Manage', [
    ['manage', 'Custom blueprints', 'package', '3']
  ]]
];

var BLUEPRINTS = [
  ['aws-estate', 'ok'],
  ['azure-csp', 'off'],
  ['edge-fleet', 'ok'],
  ['k8s-minimal', 'fail'],
  ['otlp-native', 'ok'],
  ['rum-webshop', 'ok']
];

function navItem(item, active) {
  var key = item[0], label = item[1], ic = item[2], count = item[3];
  var isActive = key === active;
  var style = 'display:flex;align-items:center;gap:8px;height:28px;padding:0 12px;' +
    (isActive
      ? 'color:var(--color-fg-default);background:var(--color-bg-selected);box-shadow:inset 2px 0 0 var(--color-bg-accent);font-weight:600'
      : 'color:var(--color-fg-soft)');
  var right = '';
  if (count === 'live2') {
    right = '<span style="' + MONO + ';font-size:11px;font-weight:600;color:var(--color-status-fail);margin-left:auto">&#9679; 2</span>';
  } else if (count) {
    right = '<span style="' + MONO + ';font-size:11px;font-variant-numeric:tabular-nums;color:' + (isActive ? 'var(--color-bg-accent)' : 'var(--color-fg-muted)') + ';margin-left:auto">' + count + '</span>';
  }
  return '<a href="#" ' + (isActive ? 'aria-current="page" ' : '') + 'style="' + style + '">' + icon(ic, 15) + '<span>' + label + '</span>' + right + '</a>';
}

function bpItem(bp, active) {
  var name = bp[0], state = bp[1];
  var isActive = ('bp:' + name) === active;
  var dot = state === 'ok' ? 'var(--color-status-ok)' : state === 'fail' ? 'var(--color-status-fail)' : 'var(--color-bg-track)';
  var style = 'display:flex;align-items:center;gap:8px;height:26px;padding:0 12px;' + MONO + ';font-size:12.5px;' +
    (isActive
      ? 'color:var(--color-fg-default);background:var(--color-bg-selected);box-shadow:inset 2px 0 0 var(--color-bg-accent);font-weight:600'
      : state === 'off' ? 'color:var(--color-fg-muted)' : 'color:var(--color-fg-soft)');
  return '<a href="#" ' + (isActive ? 'aria-current="page" ' : '') + 'style="' + style + '">' +
    '<span style="width:6px;height:6px;flex:none;border-radius:var(--radius-full);background:' + dot + '"></span><span>' + name + '</span>' +
    (state === 'off' ? '<span style="margin-left:auto;font-size:10px;text-transform:uppercase;letter-spacing:0.13em">off</span>' : '') + '</a>';
}

function postureBlock(rows) {
  rows = rows || [['live', 'db-pressure'], ['mod', 'volume 2&times;'], ['mod', 'mine-api &rarr; 8'], ['off', 'azure-csp']];
  var out = '<div style="border-top:1px solid var(--color-border-default);padding:10px 12px">' +
    '<div style="' + LBL + ';margin-bottom:6px">Posture</div><div style="display:flex;flex-direction:column;gap:5px">';
  for (var i = 0; i < rows.length; i++) {
    out += '<div style="display:flex;gap:6px;align-items:baseline">' + statusWord(rows[i][0]) +
      '<span style="' + MONO + ';font-size:11px;color:' + (rows[i][0] === 'off' ? 'var(--color-fg-muted)' : 'var(--color-fg-soft)') + '">' + rows[i][1] + '</span></div>';
  }
  return out + '</div></div>';
}

function telemetryBlock(mode) {
  var badge = mode === 'dry'
    ? '<span style="' + MONO + ';font-size:9.5px;text-transform:uppercase;letter-spacing:0.1em;padding:1px 5px;border-radius:var(--radius-control);background:var(--color-bg-inverse);color:var(--color-fg-on-inverse)">dry run</span>'
    : '<span style="' + MONO + ';font-size:9.5px;text-transform:uppercase;letter-spacing:0.1em;padding:1px 5px;border-radius:var(--radius-control);background:var(--color-bg-accent-soft);color:var(--color-bg-accent);border:1px solid var(--color-bg-accent)">live push</span>';
  function lane(word, name, ago, stats, statsColour, sparkPts) {
    return '<div><div style="display:flex;align-items:baseline;gap:6px">' + statusWord(word) +
      '<span style="' + MONO + ';font-size:11.5px;color:var(--color-fg-default)">' + name + '</span>' +
      (ago ? '<span style="' + MONO + ';font-size:10px;color:var(--color-fg-muted);margin-left:auto">' + ago + '</span>' : '') +
      '</div><div style="display:flex;align-items:center;gap:6px;' + MONO + ';font-size:10px;color:' + (statsColour || 'var(--color-fg-muted)') + ';margin-top:1px">' +
      '<span>' + stats + '</span>' + (sparkPts ? '<span style="margin-left:auto;color:var(--color-fg-muted)">' + spark(sparkPts, 40, 12) + '</span>' : '') +
      '</div></div>';
  }
  return '<div style="border-top:1px solid var(--color-border-default);padding:10px 12px">' +
    '<div style="display:flex;align-items:center;gap:6px;margin-bottom:6px"><span style="' + LBL + '">Telemetry</span>' + badge + '</div>' +
    '<div style="display:flex;flex-direction:column;gap:7px">' +
    lane('ok', 'metrics', '4s ago', '48.2k series &middot; ~9.6k/min', null, '0,8 5,7 10,9 15,5 20,6 25,4 30,5 35,3 40,4') +
    lane('ok', 'logs', '4s ago', '12.4k lines &middot; ~2.1k/min', null, null) +
    lane('fail', 'traces', '9s ago', 'rate limited &middot; 3 failed pushes', 'var(--color-status-fail)', null) +
    lane('idle', 'rum', '', 'pending &middot; no beacons yet', null, null) +
    '</div></div>';
}

function sidebar(active, opts) {
  opts = opts || {};
  var out = '<aside style="display:flex;flex-direction:column;background:var(--color-bg-surface);border-right:1px solid var(--color-border-default);overflow:hidden">' +
    '<div style="display:flex;align-items:center;gap:8px;height:48px;padding:0 12px;border-bottom:1px solid var(--color-border-default);flex:none">' +
    '<span style="width:12px;height:12px;flex:none;background:var(--color-bg-accent)"></span>' +
    '<span style="font-size:15px;font-weight:600;letter-spacing:-0.01em">synthkit</span>' +
    '<span style="' + MONO + ';font-size:10px;color:var(--color-fg-muted);margin-left:auto">v1</span></div>' +
    '<div style="flex:1;overflow:hidden;padding-bottom:8px">';
  for (var g = 0; g < NAV.length; g++) {
    out += '<div style="' + LBL + ';padding:14px 12px 4px">' + NAV[g][0] + '</div>';
    for (var i = 0; i < NAV[g][1].length; i++) out += navItem(NAV[g][1][i], active);
  }
  out += '<div style="' + LBL + ';padding:14px 12px 4px">Blueprints</div>';
  for (var b = 0; b < BLUEPRINTS.length; b++) out += bpItem(BLUEPRINTS[b], active);
  out += '</div>' + postureBlock(opts.posture) + telemetryBlock(opts.mode) + '</aside>';
  return out;
}

// ── top bar / status bar / stat strip ──────────────────────────────────────
function topbar(o) {
  return '<header style="display:flex;align-items:center;gap:12px;padding:0 20px;background:var(--color-bg-surface);border-bottom:1px solid var(--color-border-default)">' +
    '<h2 style="font-size:18px;font-weight:600;margin:0;letter-spacing:-0.01em' + (o.titleMono ? ';' + MONO : '') + '">' + o.title + '</h2>' +
    '<span style="' + MONO + ';font-size:11px;color:var(--color-fg-muted)">' + o.crumb + '</span>' +
    (o.status ? '<span style="margin-left:8px">' + o.status + '</span>' : '') +
    '<span style="margin-left:auto;' + MONO + ';font-size:11px;color:var(--color-fg-muted)">' + (o.poll || 'polled 3s ago') + '</span>' +
    (o.extra || '') +
    iconBtn('arrows-clockwise', 'Reload state') +
    iconBtn(o.theme === 'dark' ? 'sun' : 'moon', o.theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme') +
    (o.primary || '') +
    '</header>';
}

function statusbar(o) {
  o = o || {};
  return '<footer role="status" aria-live="polite" style="display:flex;align-items:center;gap:10px;padding:0 12px;background:var(--color-bg-surface);border-top:1px solid var(--color-border-default);' + MONO + ';font-size:10.5px;color:var(--color-fg-muted)">' +
    '<span style="color:' + (o.mode === 'dry' ? 'var(--color-fg-muted)' : 'var(--color-status-ok)') + '">' + (o.mode === 'dry' ? '&#9632; DRY' : '&#9632; OK') + '</span>' +
    '<span>' + (o.mode === 'dry' ? 'dry run, nothing pushed' : 'live push') + '</span><span>&middot;</span>' +
    '<span>token: ' + (o.token || 'set (user control)') + '</span><span>&middot;</span>' +
    '<span>state: ' + (o.state || 'writable') + '</span>' +
    (o.note ? '<span>&middot;</span><span>' + o.note + '</span>' : '') +
    '<span style="margin-left:auto">&#8984;K search</span></footer>';
}

function statStrip(cells) {
  var out = '<div style="display:flex;background:var(--color-bg-canvas);border-bottom:1px solid var(--color-border-default);flex:none">';
  for (var i = 0; i < cells.length; i++) {
    var c = cells[i];
    out += '<div style="padding:12px 20px;min-width:150px' + (i < cells.length - 1 ? ';border-right:1px solid var(--color-border-default)' : '') + '">' +
      '<div style="' + LBL + '">' + c[0] + '</div>' +
      '<div style="font-size:24px;font-weight:600;font-variant-numeric:tabular-nums;line-height:1.2;margin-top:2px;color:' + (c[3] || 'var(--color-fg-default)') + '">' + c[1] + '</div>' +
      (c[2] ? '<div style="' + MONO + ';font-size:11px;color:var(--color-fg-muted);margin-top:1px">' + c[2] + '</div>' : '') +
      '</div>';
  }
  return out + '</div>';
}

// ── table primitives ───────────────────────────────────────────────────────
function th(label, align, w) {
  return '<th style="text-align:' + (align || 'left') + ';' + MONO + ';font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.13em;color:var(--color-fg-muted);padding:0 12px 6px 0;border-bottom:1px solid var(--color-border-strong);white-space:nowrap' + (w ? ';width:' + w : '') + '">' + label + '</th>';
}

function td(content, o) {
  o = o || {};
  return '<td style="padding:0 12px 0 0;border-bottom:1px solid var(--color-border-default);vertical-align:middle' +
    (o.align ? ';text-align:' + o.align : '') + (o.mono ? ';' + MONO + ';font-size:12.5px' : '') +
    (o.colour ? ';color:' + o.colour : '') + (o.extra ? ';' + o.extra : '') + '">' + content + '</td>';
}

function tr(cells, o) {
  o = o || {};
  return '<tr style="height:36px' + (o.hover ? ';background:var(--color-bg-hover)' : '') + (o.selected ? ';background:var(--color-bg-selected);box-shadow:inset 2px 0 0 var(--color-bg-accent)' : '') + '">' + cells.join('') + '</tr>';
}

function table(head, rows) {
  return '<table style="width:100%;border-collapse:collapse;font-size:12.5px"><thead><tr>' + head.join('') + '</tr></thead><tbody>' + rows.join('') + '</tbody></table>';
}

function panelNote(text) {
  return '<p style="font-size:12.5px;line-height:1.55;margin:0 0 12px;color:var(--color-fg-soft);max-width:760px;text-wrap:pretty">' + text + '</p>';
}

// ── frame + page ───────────────────────────────────────────────────────────
function frame(o) {
  var dark = o.theme === 'dark';
  return '<div><div style="' + LBLP + ';margin-bottom:8px">' + (dark ? 'Dark &middot; first class' : 'Light &middot; default') + '</div>' +
    '<div ' + (dark ? 'data-theme="dark" ' : '') + 'data-screen-label="' + o.label + '" style="width:1440px;height:900px;display:grid;grid-template-columns:200px 1fr;background:var(--color-bg-canvas);color:var(--color-fg-default);font-size:13.5px;border:1px solid ' + (dark ? 'oklch(0.4 0.014 227)' : 'oklch(0.78 0.01 227)') + ';overflow:hidden;position:relative">' +
    sidebar(o.active, { posture: o.posture, mode: o.mode }) +
    '<div style="display:grid;grid-template-rows:48px 1fr 24px;min-width:0">' +
    topbar({ title: o.title, titleMono: o.titleMono, crumb: o.crumb, status: o.headStatus, poll: o.poll, primary: o.primary, extra: o.extra, theme: o.theme }) +
    '<main style="overflow:hidden;display:flex;flex-direction:column;min-height:0">' + o.content + '</main>' +
    statusbar(o.statusbar) + '</div>' + (o.overlay || '') + '</div></div>';
}

function page(o) {
  var head = '<!DOCTYPE html>\n<html>\n<head>\n<meta charset="utf-8">\n<meta name="viewport" content="width=device-width, initial-scale=1">\n<script src="./support.js"></script>\n</head>\n<body>\n<x-dc>\n' +
    '<helmet>\n  <meta name="design_doc_mode" content="canvas">\n' +
    '  <link rel="stylesheet" href="_ds/m7kni-design-system-v2-a03561c4-ff48-4ee7-8eb6-2f2a21c7ee72/tokens/tokens.css">\n' +
    '  <link rel="stylesheet" href="_ds/m7kni-design-system-v2-a03561c4-ff48-4ee7-8eb6-2f2a21c7ee72/styles/fonts.css">\n' +
    '  <link rel="stylesheet" href="_ds/m7kni-design-system-v2-a03561c4-ff48-4ee7-8eb6-2f2a21c7ee72/styles.css">\n' +
    '  <script src="icons/sprite.js"></script>\n' +
    '  <style>\n    body { margin: 0; background: oklch(0.905 0.006 227); }\n    a { color: var(--color-bg-accent); text-decoration: none; }\n    a:hover { color: var(--color-accent-hover); text-decoration: underline; }\n  </style>\n</helmet>\n\n';
  var body = '<div style="padding:40px;font-family:var(--font-family-sans);color:oklch(0.24 0.012 227)">' +
    '<div style="max-width:760px;margin:0 0 32px">' +
    '<div style="' + LBLP + '">Screen ' + o.num + ' &middot; ' + o.kicker + '</div>' +
    '<h1 style="font-size:24px;font-weight:600;margin:6px 0 8px;letter-spacing:-0.01em">' + o.title + '</h1>' +
    '<p style="font-size:13.5px;line-height:1.5;margin:0;color:oklch(0.37 0.015 227);text-wrap:pretty">' + o.intro + '</p></div>' +
    '<div style="display:flex;gap:40px;align-items:flex-start">' + o.frames + '</div>' +
    '<div style="max-width:900px;margin:32px 0 0"><div style="' + LBLP + ';margin-bottom:8px">Accessibility</div>' +
    '<p style="font-size:12.5px;line-height:1.6;margin:0;color:oklch(0.37 0.015 227);text-wrap:pretty">' + o.a11y + '</p></div>' +
    '</div>';
  return head + body + '\n</x-dc>\n<script data-dc-script type="text/plain"></script>\n</body>\n</html>\n';
}

function mono(text, size) {
  return '<span style="' + MONO + ';font-size:' + (size || 11.5) + 'px">' + text + '</span>';
}
