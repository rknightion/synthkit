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