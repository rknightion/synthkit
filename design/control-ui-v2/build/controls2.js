function switchCtl(on) {
  return '<span style="display:inline-flex;align-items:center;gap:8px">' +
    '<span style="width:26px;height:14px;flex:none;border-radius:var(--radius-full);background:' + (on ? 'var(--color-bg-accent)' : 'var(--color-bg-track)') + ';position:relative">' +
    '<span style="position:absolute;top:2px;' + (on ? 'right:2px' : 'left:2px') + ';width:10px;height:10px;border-radius:var(--radius-full);background:var(--color-bg-raised)"></span></span>' +
    '<span style="' + MONO + ';font-size:11px;color:var(--color-fg-muted);width:20px">' + (on ? 'on' : 'off') + '</span></span>';
}
function stepper(value, meta, changed) {
  return '<span style="display:inline-flex;align-items:center;gap:0">' +
    '<button type="button" style="width:22px;height:22px;border:1px solid var(--color-border-strong);border-right:0;border-radius:var(--radius-control) 0 0 var(--radius-control);background:transparent;color:var(--color-fg-soft);cursor:pointer;font-size:12px">&minus;</button>' +
    '<span style="min-width:34px;height:22px;display:inline-flex;align-items:center;justify-content:center;border:1px solid var(--color-border-strong);' + MONO + ';font-size:12.5px;font-variant-numeric:tabular-nums;color:' + (changed ? 'var(--color-bg-accent)' : 'var(--color-fg-default)') + ';font-weight:600">' + value + '</span>' +
    '<button type="button" style="width:22px;height:22px;border:1px solid var(--color-border-strong);border-left:0;border-radius:0 var(--radius-control) var(--radius-control) 0;background:transparent;color:var(--color-fg-soft);cursor:pointer;font-size:12px">+</button>' +
    '<span style="' + MONO + ';font-size:10px;color:var(--color-fg-muted);margin-left:8px">' + meta + '</span></span>';
}