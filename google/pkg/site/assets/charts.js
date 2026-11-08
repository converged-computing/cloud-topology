/* charts.js: D3 charts for the cloud topology site. Colors come from the CSS palette
   tokens (fixed categorical slots, one sequential hue, reserved status colors). */
window.Charts = (function () {
  'use strict';
  const token = name => getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  const slot = i => token('--series-' + i) || '#2a78d6';
  const status = { ok: '--status-good', stockout: '--status-warning', quota: '--status-serious', error: '--status-critical' };
  const statusColor = s => token(status[s] || '--deemph');
  const statusIcon = { ok: '✓', stockout: '◔', quota: '⊘', error: '✕' };
  const fmtSecs = s => s == null || s === '' ? '' : s < 60 ? Math.round(s) + 's' : s < 3600 ? Math.floor(s / 60) + 'm' + String(Math.round(s % 60)).padStart(2, '0') + 's' : Math.floor(s / 3600) + 'h' + String(Math.round((s % 3600) / 60)).padStart(2, '0') + 'm';
  const fmtBytes = b => b >= 1 << 30 ? (b / (1 << 30)).toFixed(b % (1 << 30) ? 1 : 0) + ' GB' : b >= 1 << 20 ? Math.round(b / (1 << 20)) + ' MB' : b >= 1024 ? Math.round(b / 1024) + ' KB' : b + ' B';
  const width = (el, fallback) => el.clientWidth || el.getBoundingClientRect().width || fallback || 900;
  const textW = (s, size) => String(s).length * (size || 12) * 0.56;
  // Transitions on SVG transforms need the SVG DOM (missing in some headless renderers).
  const canAnimate = (() => { try { return !!document.createElementNS('http://www.w3.org/2000/svg', 'g').transform.baseVal; } catch (e) { return false; } })();
  const tr = sel => canAnimate ? sel.transition().duration(250) : sel;

  // ---- tooltip (one for the page; built with textContent, never markup) ----
  const tip = document.getElementById('tip');
  function showTip(event, rows) {
    tip.replaceChildren();
    rows.forEach(r => {
      if (typeof r === 'string') { const d = document.createElement('div'); d.className = 'v'; d.textContent = r; tip.appendChild(d); return; }
      const d = document.createElement('div'); d.className = 'row';
      const k = document.createElement('span'); k.className = 'k';
      if (r.color) { const key = document.createElement('span'); key.className = 'key'; key.style.background = r.color; k.appendChild(key); }
      k.appendChild(document.createTextNode(r.k || ''));
      const v = document.createElement('span'); v.className = 'v'; v.textContent = r.v == null ? '' : r.v;
      d.appendChild(k); d.appendChild(v); tip.appendChild(d);
    });
    tip.hidden = false;
    moveTip(event);
  }
  function moveTip(event) {
    const pad = 14, w = tip.offsetWidth, h = tip.offsetHeight;
    let x = event.clientX + pad, y = event.clientY + pad;
    if (x + w > window.innerWidth - 8) x = event.clientX - w - pad;
    if (y + h > window.innerHeight - 8) y = event.clientY - h - pad;
    tip.style.left = x + 'px'; tip.style.top = y + 'px';
  }
  const hideTip = () => { tip.hidden = true; };

  function svgIn(el, w, h) {
    el.replaceChildren();
    return d3.select(el).append('svg').attr('viewBox', [0, 0, w, h]).attr('width', w).attr('height', h);
  }
  // A bar with a 4px rounded data end and a square baseline end (horizontal, growing right).
  function barPath(x, y, w, h, r) {
    r = Math.min(r, w, h / 2);
    if (w <= 0) return '';
    return `M${x},${y}h${w - r}a${r},${r} 0 0 1 ${r},${r}v${h - 2 * r}a${r},${r} 0 0 1 ${-r},${r}h${-(w - r)}z`;
  }

  // ---- horizontal bars, one series (slot 1) ----
  function bars(el, data, o) {
    o = Object.assign({ label: d => d.label, value: d => d.value, format: d3.format(','), color: slot(1), colorOf: null, max: 20, other: true, sort: true }, o || {});
    data = o.sort ? data.slice().sort((a, b) => o.value(b) - o.value(a)) : data.slice();
    let rest = 0;
    // With other: false the bars past max are left out, not summed into one bar that
    // dwarfs the rest; the caller says what was left out.
    if (data.length > o.max) { if (o.other) rest = d3.sum(data.slice(o.max - 1), o.value); data = data.slice(0, o.other ? o.max - 1 : o.max); }
    const rows = data.map(d => ({ label: o.label(d), value: o.value(d), d }));
    if (rest) rows.push({ label: 'Other', value: rest, other: true });
    const W = width(el, 600), labelW = Math.min(180, d3.max(rows, r => textW(r.label)) + 12), m = { t: 6, r: 56, b: 24, l: labelW }, rowH = 26, H = m.t + m.b + rows.length * rowH;
    const svg = svgIn(el, W, H);
    const x = d3.scaleLinear().domain([0, d3.max(rows, r => r.value) || 1]).nice().range([m.l, W - m.r]);
    const y = d3.scaleBand().domain(rows.map(r => r.label)).range([m.t, H - m.b]).paddingInner(0.3);
    svg.append('g').attr('class', 'axis').attr('transform', `translate(0,${H - m.b})`).call(d3.axisBottom(x).ticks(Math.min(6, Math.floor((W - m.l) / 80))).tickFormat(d3.format('~s')).tickSize(0).tickPadding(6)).call(g => g.select('.domain').remove());
    svg.append('g').selectAll('line').data(x.ticks(6)).join('line').attr('class', 'gridline').attr('x1', d => x(d)).attr('x2', d => x(d)).attr('y1', m.t).attr('y2', H - m.b);
    svg.append('g').selectAll('text').data(rows).join('text').attr('x', m.l - 8).attr('y', r => y(r.label) + y.bandwidth() / 2).attr('dy', '0.35em').attr('text-anchor', 'end').attr('class', 'primary').text(r => r.label.length > 26 ? r.label.slice(0, 25) + '…' : r.label);
    const bh = Math.min(24, y.bandwidth());
    svg.append('g').selectAll('path').data(rows).join('path').attr('d', r => barPath(x(0), y(r.label) + (y.bandwidth() - bh) / 2, x(r.value) - x(0), bh, 4)).attr('fill', r => r.other ? token('--deemph') : o.colorOf ? o.colorOf(r.d) : o.color)
      .on('pointerenter pointermove', (e, r) => showTip(e, [r.label, { k: o.valueLabel || 'value', v: o.format(r.value) }])).on('pointerleave', hideTip)
      .on('click', (e, r) => { if (o.onClick && !r.other) o.onClick(r.d); }).style('cursor', o.onClick ? 'pointer' : null);
    const top = rows[0];
    if (top) svg.append('text').attr('x', x(top.value) + 6).attr('y', y(top.label) + y.bandwidth() / 2).attr('dy', '0.35em').attr('class', 'primary').text(o.format(top.value));
    return svg;
  }

  // ---- stacked status bars: one bar per group, segments per status (reserved status colors + icon labels) ----
  function stackedStatus(el, groups, o) {
    o = Object.assign({ statuses: ['ok', 'stockout', 'quota', 'error', 'other'], labels: { ok: 'captured', stockout: 'stockout', quota: 'quota', error: 'error', other: 'not captured' } }, o || {});
    const W = width(el, 600), m = { t: 6, r: 16, b: 24, l: 120 }, rowH = 34, H = m.t + m.b + groups.length * rowH;
    const svg = svgIn(el, W, H);
    const total = d3.max(groups, g => d3.sum(o.statuses, s => g.counts[s] || 0)) || 1;
    const x = d3.scaleLinear().domain([0, total]).range([m.l, W - m.r]);
    const y = d3.scaleBand().domain(groups.map(g => g.name)).range([m.t, H - m.b]).paddingInner(0.35);
    svg.append('g').attr('class', 'axis').attr('transform', `translate(0,${H - m.b})`).call(d3.axisBottom(x).ticks(Math.min(total, 8)).tickSize(0).tickPadding(6)).call(g => g.select('.domain').remove());
    svg.append('g').selectAll('text').data(groups).join('text').attr('x', m.l - 8).attr('y', g => y(g.name) + y.bandwidth() / 2).attr('dy', '0.35em').attr('text-anchor', 'end').attr('class', 'primary').text(g => g.name);
    const bh = Math.min(24, y.bandwidth());
    groups.forEach(g => {
      let acc = 0;
      o.statuses.forEach(s => {
        const n = g.counts[s] || 0; if (!n) return;
        const x0 = x(acc) + (acc ? 2 : 0), x1 = x(acc + n), w = Math.max(0, x1 - x0), yy = y(g.name) + (y.bandwidth() - bh) / 2;
        const color = s === 'other' ? token('--deemph') : statusColor(s);
        svg.append('path').attr('d', barPath(x0, yy, w, bh, 4)).attr('fill', color)
          .on('pointerenter pointermove', e => showTip(e, [g.name, { k: (statusIcon[s] || '') + ' ' + o.labels[s], v: n }])).on('pointerleave', hideTip);
        const label = (statusIcon[s] || '') + ' ' + n;
        if (textW(label, 11) + 10 < w) svg.append('text').attr('class', 'in-fill' + (s === 'stockout' ? ' ink' : '')).attr('x', x0 + w / 2).attr('y', yy + bh / 2).attr('dy', '0.35em').attr('text-anchor', 'middle').text(label);
        acc += n;
      });
    });
    return svg;
  }

  // ---- scatter with emphasis (one family highlighted, the rest de-emphasized), shapes for GPUs, nearest-point hover ----
  function scatter(el, points, o) {
    o = Object.assign({ x: d => d.x, y: d => d.y, xLabel: 'x', yLabel: 'y', log: true, emphasis: null, isAlt: () => false, name: d => d.name, onClick: null, onHover: null, height: 420 }, o || {});
    const W = width(el, 800), H = o.height, m = { t: 14, r: 20, b: 40, l: 56 };
    const svg = svgIn(el, W, H);
    const pts = points.filter(d => o.x(d) > 0 && o.y(d) > 0);
    const x = (o.log ? d3.scaleLog() : d3.scaleLinear()).domain(d3.extent(pts, o.x)).nice().range([m.l, W - m.r]);
    // nice: false fits the axis to the data, where rounding a log axis out to the next
    // power of ten would leave most of the chart empty.
    const yExt = d3.extent(pts, o.y), y = (o.log ? d3.scaleLog() : d3.scaleLinear()).domain(o.nice === false && o.log ? [yExt[0] / 1.25, yExt[1] * 1.25] : yExt).range([H - m.b, m.t]);
    if (o.nice !== false) y.nice();
    const fmt = d3.format('~s');
    svg.append('g').selectAll('line').data(y.ticks(6)).join('line').attr('class', 'gridline').attr('x1', m.l).attr('x2', W - m.r).attr('y1', d => y(d)).attr('y2', d => y(d));
    svg.append('g').attr('class', 'axis').attr('transform', `translate(0,${H - m.b})`).call(d3.axisBottom(x).ticks(8, fmt).tickSize(0).tickPadding(8)).call(g => g.select('.domain').attr('stroke', token('--axis')));
    svg.append('g').attr('class', 'axis').attr('transform', `translate(${m.l},0)`).call(d3.axisLeft(y).ticks(6, fmt).tickSize(0).tickPadding(8)).call(g => g.select('.domain').remove());
    svg.append('text').attr('x', W - m.r).attr('y', H - 6).attr('text-anchor', 'end').text(o.xLabel);
    svg.append('text').attr('x', m.l).attr('y', m.t - 2).text(o.yLabel);
    const emph = d => !o.emphasis || o.emphasis(d);
    const sym = d3.symbol().size(96);
    const g = svg.append('g');
    const marks = g.selectAll('path').data(pts.slice().sort((a, b) => emph(a) - emph(b))).join('path')
      .attr('transform', d => `translate(${x(o.x(d))},${y(o.y(d))})`)
      .attr('d', d => sym.type(o.isAlt(d) ? d3.symbolDiamond : d3.symbolCircle)())
      .attr('fill', d => emph(d) ? (o.colorOf ? o.colorOf(d) : slot(1)) : token('--deemph')).attr('fill-opacity', d => emph(d) ? 0.9 : 0.55)
      .attr('stroke', token('--surface-1')).attr('stroke-width', 2);
    // Nearest-point hover: a Delaunay layer so the pointer only has to be closest.
    const delaunay = d3.Delaunay.from(pts, d => x(o.x(d)), d => y(o.y(d)));
    let last = null;
    svg.append('rect').attr('x', m.l).attr('y', m.t).attr('width', W - m.l - m.r).attr('height', H - m.t - m.b).attr('fill', 'transparent').style('cursor', o.onClick ? 'pointer' : null)
      .on('pointermove', e => {
        const [px, py] = d3.pointer(e); const i = delaunay.find(px, py); const d = pts[i];
        if (!d) return;
        if (d !== last) { marks.attr('stroke', q => q === d ? token('--text-primary') : token('--surface-1')).attr('stroke-width', q => q === d ? 2 : 2); last = d; if (o.onHover) o.onHover(d); }
        showTip(e, [o.name(d), { k: o.xLabel, v: fmt(o.x(d)) }, { k: o.yLabel, v: d3.format(',')(o.y(d)) }].concat(o.extra ? o.extra(d) : []));
      })
      .on('pointerleave', () => { hideTip(); marks.attr('stroke', token('--surface-1')); last = null; if (o.onHover) o.onHover(null); })
      .on('click', () => { if (last && o.onClick) o.onClick(last); });
    return svg;
  }

  // ---- hwloc icicle: zoomable partition of the object tree, root at the left ----
  const typeColors = { Package: () => slot(1), NUMANode: () => slot(3), Core: () => slot(7), PU: () => slot(7), L3Cache: () => slot(4), L2Cache: () => slot(4), L1Cache: () => slot(4), L1iCache: () => slot(4), Machine: () => token('--deemph'), Group: () => token('--deemph'), Die: () => slot(1) };
  function objColor(d) {
    const t = d.data.t;
    if (t === 'PCIDev' || t === 'OSDev') {
      const a = d.data.a || {}, cls = (a.pci_type || '').split(' ')[0].replace('0x', '').replace(/[^0-9a-f]/gi, '');
      const parent = d.parent && d.parent.data.t === 'PCIDev' ? d.parent.data : d.data, pa = parent.a || {}, pcls = (pa.pci_type || '').split(' ')[0];
      const kind = a.osdev_type || (pcls.startsWith('03') || cls.startsWith('03') ? 'gpu' : pcls.startsWith('02') || cls.startsWith('02') ? 'network' : pcls.startsWith('01') || cls.startsWith('01') ? 'block' : 'other');
      return kind === 'gpu' ? slot(2) : kind === 'network' ? slot(5) : kind === 'block' ? slot(6) : token('--deemph');
    }
    return (typeColors[t] || (() => token('--deemph')))();
  }
  const objOpacity = d => ({ PU: 0.55, L2Cache: 0.75, L1Cache: 0.5, L1iCache: 0.4, OSDev: 0.7 }[d.data.t] || 0.95);
  const idx = d => d.data.i || 0;
  const objLabel = d => d.data.t === 'PU' ? 'PU ' + idx(d) : d.data.t === 'Core' ? 'Core ' + idx(d) : d.data.t === 'Package' ? 'Package ' + idx(d) : d.data.t === 'NUMANode' ? 'NUMA ' + idx(d) + (d.data.s ? ' · ' + fmtBytes(d.data.s) : '') : d.data.t.endsWith('Cache') ? d.data.t.replace('Cache', '') + (d.data.s ? ' ' + fmtBytes(d.data.s) : '') : d.data.t === 'PCIDev' ? ((d.data.a || {}).PCIDevice || d.data.n || 'PCI device') : d.data.t === 'OSDev' ? (d.data.n || 'device') : d.data.t === 'Machine' ? 'Machine' + (d.data.s ? ' · ' + fmtBytes(d.data.s) : '') : d.data.t + ' ' + idx(d);
  function objRows(d) {
    const rows = [objLabel(d), { k: 'type', v: d.data.t }];
    if (d.data.n) rows.push({ k: 'name', v: d.data.n });
    if (d.data.s) rows.push({ k: 'size', v: fmtBytes(d.data.s) });
    Object.entries(d.data.a || {}).forEach(([k, v]) => rows.push({ k, v }));
    const leaves = d.leaves().filter(l => l.data.t === 'PU').length; if (leaves && d.data.t !== 'PU') rows.push({ k: 'PUs below', v: leaves });
    return rows;
  }
  function icicle(el, hwloc, o) {
    o = Object.assign({ height: 360, onSelect: null }, o || {});
    const wrap = document.createElement('div'); el.replaceChildren(wrap);
    const crumbs = document.createElement('div'); crumbs.className = 'breadcrumb'; wrap.appendChild(crumbs);
    const chart = document.createElement('div'); chart.className = 'chart icicle'; wrap.appendChild(chart);
    const legend = document.createElement('div'); legend.className = 'legend'; wrap.appendChild(legend);
    [['Package', slot(1)], ['NUMA memory', slot(3)], ['Cache', slot(4)], ['Core / PU', slot(7)], ['GPU', slot(2)], ['Network', slot(5)], ['Storage', slot(6)]].forEach(([l, c]) => { const s = document.createElement('span'); const sw = document.createElement('span'); sw.className = 'sw'; sw.style.background = c; s.appendChild(sw); s.appendChild(document.createTextNode(l)); legend.appendChild(s); });
    const root = d3.hierarchy(hwloc, d => d.c).sum(d => d.c && d.c.length ? 0 : 1).sort((a, b) => (a.data.t === 'NUMANode') - (b.data.t === 'NUMANode') || (a.data.i || 0) - (b.data.i || 0));
    let focus = root;
    const W = width(chart, 700), H = o.height;
    function draw() {
      const sub = d3.hierarchy(focus.data, d => d.c).sum(d => d.c && d.c.length ? 0 : 1).sort((a, b) => (a.data.t === 'NUMANode') - (b.data.t === 'NUMANode') || (a.data.i || 0) - (b.data.i || 0));
      d3.partition().size([H, W]).padding(1)(sub);
      const svg = svgIn(chart, W, H);
      const cell = svg.selectAll('g').data(sub.descendants()).join('g').attr('transform', d => `translate(${d.y0},${d.x0})`);
      cell.append('rect').attr('width', d => Math.max(0, d.y1 - d.y0)).attr('height', d => Math.max(0, d.x1 - d.x0)).attr('rx', 2)
        .attr('fill', d => objColor(d)).attr('fill-opacity', d => objOpacity(d))
        .on('pointerenter pointermove', (e, d) => showTip(e, objRows(d))).on('pointerleave', hideTip)
        .on('click', (e, d) => { if (d.children) { focus = findIn(root, d.data); draw(); } if (o.onSelect) o.onSelect(d); });
      cell.filter(d => d.x1 - d.x0 > 14 && d.y1 - d.y0 > 40).append('text').attr('class', d => 'in-fill' + (d.data.t.endsWith('Cache') ? ' ink' : '')).attr('x', 5).attr('y', 4).attr('dy', '0.9em')
        .text(d => { const l = objLabel(d); const max = Math.floor((d.y1 - d.y0 - 8) / 6.3); return l.length > max ? (max > 3 ? l.slice(0, max - 1) + '…' : '') : l; });
      crumbs.replaceChildren();
      const path = []; let n = focus; while (n) { path.unshift(n); n = n.parent; }
      path.forEach((p, i) => { if (i) { const s = document.createElement('span'); s.className = 'sep'; s.textContent = '›'; crumbs.appendChild(s); } const b = document.createElement('button'); b.type = 'button'; b.textContent = objLabel(p); b.disabled = p === focus; b.onclick = () => { focus = p; draw(); }; crumbs.appendChild(b); });
    }
    function findIn(node, data) { if (node.data === data) return node; for (const c of node.children || []) { const f = findIn(c, data); if (f) return f; } return node; }
    draw();
    return { refocus: () => { focus = root; draw(); } };
  }

  // ---- folded Fluxion graph as a collapsible, pannable tree ----
  function graphTree(el, tree, o) {
    o = Object.assign({ depth: 2, query: '', onSelect: null }, o || {});
    const W = width(el, 900), H = Math.max(420, Math.round(window.innerHeight * 0.7));
    const svg = svgIn(el, W, H).attr('class', 'gtree').attr('height', H);
    const g = svg.append('g');
    const root = d3.hierarchy(tree, d => d.children);
    root.each(d => { d.id = d.data.type + ':' + d.data.name + ':' + (d.parent ? d.parent.id : ''); });
    const q = (o.query || '').toLowerCase();
    const matches = d => q && (d.data.name.toLowerCase().includes(q) || d.data.type.includes(q) || Object.entries(d.data.properties || {}).some(([k, v]) => (k + '=' + v).toLowerCase().includes(q)));
    root.each(d => { d.hit = matches(d); d.hasHit = false; });
    root.eachAfter(d => { d.hasHit = d.hit || (d.children || []).some(c => c.hasHit); });
    // With a query, keep matches, their ancestors and their own children (collapsed below),
    // expanded; otherwise collapse below o.depth.
    if (q) root.each(d => { if (d.children && !d.hit) { d.children = d.children.filter(c => c.hasHit); if (!d.children.length) d.children = null; } });
    root.each(d => { if (d.children && !(d.depth < o.depth || (q && d.hasHit))) { d._children = d.children; d.children = null; } });
    const layout = d3.tree().nodeSize([22, 230]);
    const link = d3.linkHorizontal().x(d => d.y).y(d => d.x);
    const zoom = d3.zoom().scaleExtent([0.2, 3]).extent([[0, 0], [W, H]]).on('zoom', e => g.attr('transform', e.transform));
    svg.call(zoom);
    let selected = null;
    function label(d) { return d.data.name + (d.data.count > 1 ? ' ×' + d3.format(',')(d.data.count) : ''); }
    function update(source) {
      layout(root);
      const nodes = root.descendants(), links = root.links();
      const minX = d3.min(nodes, d => d.x);
      const node = g.selectAll('g.node').data(nodes, d => d.id);
      const enter = node.enter().append('g').attr('class', 'node').attr('transform', d => `translate(${source ? source.y0 || 0 : d.y},${source ? source.x0 || 0 : d.x})`)
        .on('click', (e, d) => { if (d.children) { d._children = d.children; d.children = null; } else if (d._children) { d.children = d._children; d._children = null; } selected = d; if (o.onSelect) o.onSelect(d); update(d); })
        .on('pointerenter pointermove', (e, d) => showTip(e, [d.data.type + ' ' + label(d), { k: 'vertices', v: d3.format(',')(d.data.vertices) }].concat(d.data.size ? [{ k: 'size', v: d.data.size + (d.data.unit ? ' ' + d.data.unit : '') }] : []).concat(Object.entries(d.data.properties || {}).map(([k, v]) => ({ k, v })))))
        .on('pointerleave', hideTip);
      enter.append('circle').attr('r', 5);
      enter.append('text').attr('class', 'type').attr('dy', '-0.5em').attr('x', 9).text(d => d.data.type);
      enter.append('text').attr('class', 'name').attr('dy', '0.85em').attr('x', 9).text(label);
      const all = enter.merge(node);
      all.attr('class', d => 'node' + (d.hit ? ' hit' : '') + (d === selected ? ' sel' : ''));
      tr(all).attr('transform', d => `translate(${d.y},${d.x})`);
      all.select('circle').attr('fill', d => d._children ? slot(1) : d.children ? token('--surface-1') : token('--deemph')).attr('stroke', d => d._children || d.children ? slot(1) : token('--deemph'));
      all.select('text.name').attr('class', 'name primary');
      tr(node.exit()).attr('transform', d => `translate(${source.y},${source.x})`).remove();
      const l = g.selectAll('path.link').data(links, d => d.target.id);
      tr(l.enter().insert('path', 'g').attr('class', 'link').merge(l)).attr('d', link);
      l.exit().remove();
      nodes.forEach(d => { d.x0 = d.x; d.y0 = d.y; });
      if (!source) svg.call(zoom.transform, d3.zoomIdentity.translate(40, Math.max(20, H / 2 - (minX + d3.max(nodes, d => d.x)) / 2)));
    }
    update(null);
    return {
      expandAll: () => { root.each(d => { if (d._children) { d.children = d._children; d._children = null; } }); update(root); },
      collapseAll: () => { root.each(d => { if (d.children && d.depth >= 1) { d._children = d.children; d.children = null; } }); update(root); },
      reset: () => (canAnimate ? svg.transition().duration(300) : svg).call(zoom.transform, d3.zoomIdentity.translate(40, H / 2)),
    };
  }

  // ---- heatmap: rows × columns, one sequential hue; empty cells are surface with a hairline ----
  function heatmap(el, rows, cols, o) {
    o = Object.assign({ value: (r, c) => null, format: v => v, rowLabel: r => r, colLabel: c => c, onClick: null, cellH: 22, extra: null }, o || {});
    const W = width(el, 800), labelW = Math.min(220, d3.max(rows, r => textW(o.rowLabel(r))) + 14), m = { t: 48, r: o.extra ? 110 : 12, b: 8, l: labelW };
    const H = m.t + m.b + rows.length * o.cellH;
    const svg = svgIn(el, W, H);
    const x = d3.scaleBand().domain(cols).range([m.l, W - m.r]).paddingInner(0.06);
    const y = d3.scaleBand().domain(rows.map(o.rowLabel)).range([m.t, H - m.b]).paddingInner(0.1);
    const vals = []; rows.forEach(r => cols.forEach(c => { const v = o.value(r, c); if (v != null) vals.push(v); }));
    // Fixed bins on minute boundaries, so the same color means the same time on every
    // page; an analogous yellow-green-blue ramp (light is fast, dark is slow) keeps nine
    // steps apart where one hue cannot.
    const edges = o.thresholds || [30, 45, 60, 90, 120, 180, 300, 600];
    const steps = d3.schemeYlGnBu[edges.length + 1];
    const color = d3.scaleThreshold().domain(edges).range(steps);
    const ink = v => d3.hsl(color(v)).l > 0.55 ? '#0b0b0b' : '#fff';
    svg.append('g').selectAll('text').data(cols).join('text').attr('x', c => x(c) + x.bandwidth() / 2).attr('y', m.t - 10).attr('text-anchor', 'middle').attr('class', 'primary').text(c => { const l = o.colLabel(c); const max = Math.floor(x.bandwidth() / 6.5); return l.length > max ? l.slice(0, Math.max(3, max - 1)) + '…' : l; });
    svg.append('g').selectAll('text').data(rows).join('text').attr('x', m.l - 8).attr('y', r => y(o.rowLabel(r)) + y.bandwidth() / 2).attr('dy', '0.35em').attr('text-anchor', 'end').attr('class', 'primary').text(r => o.rowLabel(r)).style('cursor', o.onClick ? 'pointer' : null).on('click', (e, r) => o.onClick && o.onClick(r));
    rows.forEach(r => cols.forEach(c => {
      const v = o.value(r, c), yy = y(o.rowLabel(r)), xx = x(c);
      const cell = svg.append('rect').attr('x', xx).attr('y', yy).attr('width', x.bandwidth()).attr('height', y.bandwidth()).attr('rx', 3)
        .attr('fill', v == null ? token('--surface-1') : color(v)).attr('stroke', v == null ? token('--grid') : 'none')
        .on('pointerenter pointermove', e => showTip(e, [o.rowLabel(r), { k: o.colLabel(c), v: v == null ? 'not captured' : o.format(v) }].concat(o.tipExtra ? o.tipExtra(r, c) : []))).on('pointerleave', hideTip)
        .on('click', () => o.onClick && o.onClick(r)).style('cursor', o.onClick ? 'pointer' : null);
      if (v != null && x.bandwidth() > 44 && y.bandwidth() > 14) svg.append('text').attr('class', 'in-fill').attr('x', xx + x.bandwidth() / 2).attr('y', yy + y.bandwidth() / 2).attr('dy', '0.35em').attr('text-anchor', 'middle').style('fill', ink(v)).text(o.format(v));
    }));
    if (o.extra) svg.append('g').selectAll('text').data(rows).join('text').attr('x', W - m.r + 10).attr('y', r => y(o.rowLabel(r)) + y.bandwidth() / 2).attr('dy', '0.35em').attr('class', 'primary').text(r => o.extra(r));
    // Scale legend: one swatch per bin, labelled by its upper bound.
    if (vals.length) { const lg = svg.append('g').attr('transform', `translate(${m.l},${m.t - 44})`); let xo = 0; steps.forEach((c, k) => { const label = k === 0 ? '< ' + o.format(edges[0]) : k === steps.length - 1 ? '> ' + o.format(edges[k - 1]) : o.format(edges[k - 1]) + '–' + o.format(edges[k]); lg.append('rect').attr('x', xo).attr('y', 0).attr('width', 14).attr('height', 10).attr('rx', 2).attr('fill', c); lg.append('text').attr('x', xo + 18).attr('y', 9).text(label); xo += 28 + textW(label, 12); }); }
    return svg;
  }

  // ---- dumbbell: two values per row (slots 1 and 2), connected ----
  function dumbbell(el, rows, o) {
    o = Object.assign({ a: r => r.a, b: r => r.b, label: r => r.label, aName: 'a', bName: 'b', format: fmtSecs, onClick: null }, o || {});
    const W = width(el, 800), labelW = Math.min(220, d3.max(rows, r => textW(o.label(r))) + 14), m = { t: 10, r: 20, b: 28, l: labelW }, rowH = 24, H = m.t + m.b + rows.length * rowH;
    const svg = svgIn(el, W, H);
    const vals = rows.flatMap(r => [o.a(r), o.b(r)]).filter(v => v != null);
    // Log scale: one slow capture should not squash the rest.
    const x = d3.scaleLog().domain([Math.max(1, d3.min(vals) * 0.8), (d3.max(vals) || 2) * 1.1]).range([m.l, W - m.r]);
    const y = d3.scalePoint().domain(rows.map(o.label)).range([m.t + rowH / 2, H - m.b - rowH / 2]);
    svg.append('g').selectAll('line').data([15, 30, 60, 120, 300, 600, 1200, 1800, 3600].filter(v => v >= x.domain()[0] && v <= x.domain()[1])).join('line').attr('class', 'gridline').attr('x1', d => x(d)).attr('x2', d => x(d)).attr('y1', m.t).attr('y2', H - m.b);
    svg.append('g').attr('class', 'axis').attr('transform', `translate(0,${H - m.b})`).call(d3.axisBottom(x).tickValues([15, 30, 60, 120, 300, 600, 1200, 1800, 3600].filter(v => v >= x.domain()[0] && v <= x.domain()[1])).tickFormat(o.format).tickSize(0).tickPadding(6)).call(g => g.select('.domain').remove());
    svg.append('g').selectAll('text').data(rows).join('text').attr('x', m.l - 8).attr('y', r => y(o.label(r))).attr('dy', '0.35em').attr('text-anchor', 'end').attr('class', 'primary').text(o.label).style('cursor', o.onClick ? 'pointer' : null).on('click', (e, r) => o.onClick && o.onClick(r));
    rows.forEach(r => {
      const a = o.a(r), b = o.b(r), yy = y(o.label(r));
      if (a != null && b != null) svg.append('line').attr('x1', x(a)).attr('x2', x(b)).attr('y1', yy).attr('y2', yy).attr('stroke', token('--axis')).attr('stroke-width', 2);
      [[a, slot(1), o.aName], [b, slot(2), o.bName]].forEach(([v, c, n]) => { if (v == null) return; svg.append('circle').attr('cx', x(v)).attr('cy', yy).attr('r', 5.5).attr('fill', c).attr('stroke', token('--surface-1')).attr('stroke-width', 2)
        .on('pointerenter pointermove', e => showTip(e, [o.label(r), { k: o.aName, v: a == null ? '—' : o.format(a), color: slot(1) }, { k: o.bName, v: b == null ? '—' : o.format(b), color: slot(2) }])).on('pointerleave', hideTip); });
    });
    return svg;
  }

  // ---- timeline: lanes of intervals (one color per lane, status by fill vs outline) ----
  // With o.elapsed, start and end are seconds from each lane's own start, so lanes line up.
  function timeline(el, lanes, o) {
    o = Object.assign({ height: 0, elapsed: false }, o || {});
    const items = lanes.flatMap((l, i) => l.items.map(it => Object.assign({ lane: l.name, li: i }, it)));
    if (!items.length) { el.replaceChildren(); return; }
    const W = width(el, 900), m = { t: 8, r: 16, b: 28, l: 110 }, laneH = 34, H = m.t + m.b + lanes.length * laneH;
    const svg = svgIn(el, W, H);
    const x = (o.elapsed ? d3.scaleLinear().domain([0, d3.max(items, d => d.end)]) : d3.scaleTime().domain([d3.min(items, d => d.start), d3.max(items, d => d.end)])).nice().range([m.l, W - m.r]);
    const fmtX = o.elapsed ? (v => v >= 3600 ? (v / 3600).toFixed(v % 3600 ? 1 : 0) + 'h' : Math.round(v / 60) + 'm') : null;
    const y = d3.scaleBand().domain(lanes.map(l => l.name)).range([m.t, H - m.b]).paddingInner(0.3);
    svg.append('g').selectAll('line').data(x.ticks(8)).join('line').attr('class', 'gridline').attr('x1', d => x(d)).attr('x2', d => x(d)).attr('y1', m.t).attr('y2', H - m.b);
    svg.append('g').attr('class', 'axis').attr('transform', `translate(0,${H - m.b})`).call(d3.axisBottom(x).ticks(8).tickFormat(fmtX).tickSize(0).tickPadding(6)).call(g => g.select('.domain').remove());
    svg.append('g').selectAll('text').data(lanes).join('text').attr('x', m.l - 8).attr('y', l => y(l.name) + y.bandwidth() / 2).attr('dy', '0.35em').attr('text-anchor', 'end').attr('class', 'primary').text(l => l.name);
    if (o.elapsed) svg.append('text').attr('x', W - m.r).attr('y', H - 4).attr('text-anchor', 'end').text('time since the arm started');
    const bh = Math.min(20, y.bandwidth());
    svg.append('g').selectAll('rect').data(items).join('rect').attr('x', d => x(d.start)).attr('y', d => y(d.lane) + (y.bandwidth() - bh) / 2).attr('width', d => Math.max(3, x(d.end) - x(d.start))).attr('height', bh).attr('rx', 3)
      .attr('fill', d => d.status === 'ok' ? slot(d.li + 1) : token('--surface-1')).attr('stroke', d => slot(d.li + 1)).attr('stroke-width', d => d.status === 'ok' ? 0 : 1.5)
      .on('pointerenter pointermove', (e, d) => showTip(e, [d.label, { k: 'outcome', v: (statusIcon[d.status] || '') + ' ' + d.status }, { k: o.elapsed ? 'started at' : 'start', v: o.elapsed ? fmtSecs(d.start) + ' in' : d3.timeFormat('%H:%M:%S')(d.start) }, { k: 'duration', v: fmtSecs(o.elapsed ? d.end - d.start : (d.end - d.start) / 1000) }].concat(d.extra || []))).on('pointerleave', hideTip);
    return svg;
  }

  // ---- shape strip: one row per machine type, one dot per capture at its time, colored by shape ----
  function shapeStrip(el, types, o) {
    o = Object.assign({ onClick: null, slotOf: null }, o || {});
    const obs = types.flatMap(t => t.observations.map(x => Object.assign({ type: t.machine_type, time: new Date(x.captured_at) }, x)));
    if (!obs.length) { el.replaceChildren(); return; }
    const W = width(el, 900), labelW = Math.min(200, d3.max(types, t => textW(t.machine_type)) + 14), m = { t: 8, r: 16, b: 28, l: labelW }, rowH = 22, H = m.t + m.b + types.length * rowH;
    const svg = svgIn(el, W, H);
    const x = d3.scaleTime().domain(d3.extent(obs, d => d.time)).nice().range([m.l, W - m.r]);
    const y = d3.scalePoint().domain(types.map(t => t.machine_type)).range([m.t + rowH / 2, H - m.b - rowH / 2]);
    svg.append('g').selectAll('line').data(x.ticks(8)).join('line').attr('class', 'gridline').attr('x1', d => x(d)).attr('x2', d => x(d)).attr('y1', m.t).attr('y2', H - m.b);
    svg.append('g').selectAll('line.row').data(types).join('line').attr('class', 'gridline').attr('x1', m.l).attr('x2', W - m.r).attr('y1', t => y(t.machine_type)).attr('y2', t => y(t.machine_type));
    svg.append('g').attr('class', 'axis').attr('transform', `translate(0,${H - m.b})`).call(d3.axisBottom(x).ticks(8).tickSize(0).tickPadding(6)).call(g => g.select('.domain').remove());
    svg.append('g').selectAll('text').data(types).join('text').attr('x', m.l - 8).attr('y', t => y(t.machine_type)).attr('dy', '0.35em').attr('text-anchor', 'end').attr('class', 'primary').text(t => t.machine_type).style('cursor', o.onClick ? 'pointer' : null).on('click', (e, t) => o.onClick && o.onClick(t));
    const marks = svg.append('g').selectAll('circle').data(obs).join('circle').attr('cx', d => x(d.time)).attr('cy', d => y(d.type)).attr('r', 5)
      .attr('fill', d => slot(Math.min(8, o.slotOf(d.type, d.shape)))).attr('stroke', token('--surface-1')).attr('stroke-width', 2);
    const delaunay = d3.Delaunay.from(obs, d => x(d.time), d => y(d.type));
    svg.append('rect').attr('x', m.l).attr('y', m.t).attr('width', W - m.l - m.r).attr('height', H - m.t - m.b).attr('fill', 'transparent')
      .on('pointermove', e => { const [px, py] = d3.pointer(e); const d = obs[delaunay.find(px, py)]; if (!d) return; marks.attr('stroke', q => q === d ? token('--text-primary') : token('--surface-1'));
        showTip(e, [d.type, { k: 'shape', v: d.shape, color: slot(Math.min(8, o.slotOf(d.type, d.shape))) }, { k: 'captured', v: d.captured_at }, { k: 'database', v: o.dbName ? o.dbName(d.database) : d.database }, { k: 'zone', v: d.zone }, { k: 'model', v: d.model }, { k: 'CPU', v: (d.fingerprint.cpu_models || []).join(', ') || 'not reported' }]); })
      .on('pointerleave', () => { hideTip(); marks.attr('stroke', token('--surface-1')); })
      .style('cursor', o.onPick ? 'pointer' : null)
      .on('click', e => { const [px, py] = d3.pointer(e); const d = obs[delaunay.find(px, py)]; if (d && o.onPick) { hideTip(); o.onPick(d); } });
    return svg;
  }

  // ---- motif glyph: a small horizontal icicle of a folded subtree, one block per entry ----
  const glyphColor = { placement: () => token('--deemph'), node: () => token('--deemph'), socket: () => slot(1), core: () => slot(7), gpu: () => slot(2), nic: () => slot(5), memory: () => slot(3), local_ssd: () => slot(4) };
  function glyphFill(t) { if (glyphColor[t]) return glyphColor[t](); if (t.startsWith('pd_') || t.startsWith('hyperdisk')) return slot(6); return token('--deemph'); }
  function motifGlyph(el, shape, o) {
    o = Object.assign({ width: 240, height: 44 }, o || {});
    const root = d3.hierarchy(shape, d => d.k).sum(d => d.k && d.k.length ? 0 : 1 + Math.log2(d.c || 1));
    d3.partition().size([o.height, o.width]).padding(1)(root);
    const svg = svgIn(el, o.width, o.height);
    svg.attr('width', o.width).attr('height', o.height);
    const shift = root.y1 - root.y0, scale = o.width / (o.width - shift);
    const cell = svg.selectAll('g').data(root.descendants().filter(d => d.depth > 0)).join('g').attr('transform', d => `translate(${(d.y0 - shift) * scale},${d.x0})`);
    const w = d => Math.max(0, (d.y1 - d.y0) * scale - 1);
    cell.append('rect').attr('width', w).attr('height', d => Math.max(0, d.x1 - d.x0)).attr('rx', 1.5).attr('fill', d => glyphFill(d.data.t)).attr('fill-opacity', d => d.data.t === 'core' ? 0.75 : 0.95)
      .on('pointerenter pointermove', (e, d) => showTip(e, [d.data.t + (d.data.c > 1 ? ' ×' + d.data.c : ''), { k: 'size', v: d.data.s ? d.data.s + ' ' + (d.data.u || '') : '' }])).on('pointerleave', hideTip);
    cell.filter(d => w(d) > 26 && d.x1 - d.x0 > 9).append('text').attr('class', 'in-fill').attr('x', 3).attr('y', d => (d.x1 - d.x0) / 2).attr('dy', '0.35em').attr('font-size', 9)
      .text(d => { const l = d.data.t.replace('hyperdisk_', 'hd-').replace('local_ssd', 'lssd') + (d.data.c > 1 ? ' ×' + d.data.c : ''); const max = Math.floor((w(d) - 4) / 5.2); return l.length > max ? (max > 2 ? l.slice(0, max - 1) + '…' : '') : l; });
    return svg;
  }

  function columnPath(x, y, w, h, r) {
    r = Math.min(r, w / 2, h);
    if (h <= 0 || w <= 0) return '';
    return `M${x},${y + h}v${-(h - r)}a${r},${r} 0 0 1 ${r},${-r}h${w - 2 * r}a${r},${r} 0 0 1 ${r},${r}v${h - r}z`;
  }

  // ---- grouped columns: one group per bin, one column per series, side by side ----
  // series: [{ name, color, values: [count per bin] }]; bins: labels in order.
  function groupedColumns(el, bins, series, o) {
    o = Object.assign({ height: 260, valueLabel: 'count', binLabel: '', format: d3.format(',') }, o || {});
    const W = width(el, 600), H = o.height, m = { t: 12, r: 12, b: 44, l: 44 };
    const svg = svgIn(el, W, H);
    const x0 = d3.scaleBand().domain(bins).range([m.l, W - m.r]).paddingInner(0.2);
    const x1 = d3.scaleBand().domain(series.map(s => s.name)).range([0, x0.bandwidth()]).padding(0.08);
    const y = d3.scaleLinear().domain([0, d3.max(series, s => d3.max(s.values)) || 1]).nice().range([H - m.b, m.t]);
    svg.append('g').selectAll('line').data(y.ticks(5)).join('line').attr('class', 'gridline').attr('x1', m.l).attr('x2', W - m.r).attr('y1', d => y(d)).attr('y2', d => y(d));
    svg.append('g').attr('class', 'axis').attr('transform', `translate(0,${H - m.b})`).call(d3.axisBottom(x0).tickSize(0).tickPadding(6)).call(g => g.select('.domain').attr('stroke', token('--axis')));
    svg.append('g').attr('class', 'axis').attr('transform', `translate(${m.l},0)`).call(d3.axisLeft(y).ticks(5, '~s').tickSize(0).tickPadding(6)).call(g => g.select('.domain').remove());
    if (o.binLabel) svg.append('text').attr('x', W - m.r).attr('y', H - 6).attr('text-anchor', 'end').text(o.binLabel);
    const cells = series.flatMap(s => s.values.map((v, i) => ({ s, bin: bins[i], v })));
    svg.append('g').selectAll('path').data(cells).join('path')
      .attr('d', c => columnPath(x0(c.bin) + x1(c.s.name), y(c.v), x1.bandwidth(), y(0) - y(c.v), 3)).attr('fill', c => c.s.color)
      .on('pointerenter pointermove', (e, c) => showTip(e, [c.s.name, { k: o.binLabel || 'bin', v: c.bin }, { k: o.valueLabel, v: o.format(c.v) }])).on('pointerleave', hideTip);
    return svg;
  }

  return { groupedColumns, motifGlyph, shapeStrip, slot, token, statusColor, statusIcon, fmtSecs, fmtBytes, showTip, hideTip, bars, stackedStatus, scatter, icicle, graphTree, heatmap, dumbbell, timeline, objLabel };
})();
