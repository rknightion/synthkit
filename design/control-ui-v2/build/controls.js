function searchField(placeholder, w) {
  return '<span style="display:inline-flex;align-items:center;gap:8px;height:28px;width:' + (w || 320) + 'px;padding:0 10px;border:1px solid var(--color-border-strong);border-radius:var(--radius-control);background:var(--color-bg-raised);color:var(--color-fg-muted)">' +
    icon('magnifying-glass', 14) +
    '<span style="' + MONO + ';font-size:12.5px;color:var(--color-fg-faint);flex:1">' + placeholder + '</span>' +
    '<span style="' + MONO + ';font-size:10px;padding:1px 4px;border:1px solid var(--color-border-default);border-radius:var(--radius-control)">/</span></span>';
}
function segmented(items) {
  var out = '<span style="display:inline-flex;align-items:stretch;border:1px solid var(--color-border-strong);border-radius:var(--radius-control);overflow:hidden;height:28px">';
  for (var i = 0; i < items.length; i++) {
    var active = items[i][1];
    out += '<button type="button" style="padding:0 11px;border:0;' + (i ? 'border-left:1px solid var(--color-border-default);' : '') +
      'background:' + (active ? 'var(--color-bg-selected)' : 'transparent') + ';color:' + (active ? 'var(--color-fg-default)' : 'var(--color-fg-muted)') +
      ';font-family:var(--font-family-sans);font-size:12px;font-weight:' + (active ? '600' : '400') + ';cursor:pointer;white-space:nowrap">' + items[i][0] + '</button>';
  }
  return out + '</span>';
}
function filterRow(left, right) {
  return '<div style="display:flex;align-items:center;gap:10px;padding:12px 20px;border-bottom:1px solid var(--color-border-default);flex:none">' + left +
    '<span style="margin-left:auto;' + MONO + ';font-size:11px;color:var(--color-fg-muted)">' + right + '</span></div>';
}
function tabs(items) {
  var out = '<div style="display:flex;gap:2px;align-items:stretch;border-bottom:1px solid var(--color-border-default);padding:0 20px;background:var(--color-bg-canvas);flex:none">';
  for (var i = 0; i < items.length; i++) {
    var it = items[i], active = it[2];
    out += '<button type="button" style="height:34px;display:inline-flex;align-items:center;gap:7px;padding:0 12px;border:0;background:transparent;cursor:pointer;font-family:var(--font-family-sans);font-size:12.5px;' +
      (active ? 'color:var(--color-fg-default);font-weight:600;box-shadow:inset 0 -2px 0 var(--color-bg-accent)' : 'color:var(--color-fg-muted)') + '">' +
      '<span>' + it[0] + '</span>' + (it[1] ? '<span style="' + MONO + ';font-size:11px;font-variant-numeric:tabular-nums;color:' + (active ? 'var(--color-bg-accent)' : 'var(--color-fg-muted)') + '">' + it[1] + '</span>' : '') + '</button>';
  }
  return out + '</div>';
}