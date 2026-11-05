/* app.js: views for the cloud topology site (static; data from cmd/site). */
(function () {
  'use strict';
  const C = window.Charts;
  const inline = document.getElementById('site-data');
  const SITE = inline ? JSON.parse(inline.textContent) : null;
  const cache = {};
  async function load(path) {
    if (cache[path]) return cache[path];
    if (SITE) { cache[path] = SITE[path]; return cache[path]; }
    const r = await fetch(path);
    if (!r.ok) throw new Error(path + ': ' + r.status);
    cache[path] = await r.json();
    return cache[path];
  }

  const main = document.getElementById('main');
  const esc = s => String(s == null ? '' : s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
  const isRaw = v => v !== null && typeof v === 'object' && '__raw' in v;
  const h = (strings, ...vals) => strings.reduce((out, s, i) => out + s + (i < vals.length ? (isRaw(vals[i]) ? vals[i].__raw : esc(vals[i])) : ''), '');
  const raw = s => ({ __raw: s });
  const gb = mb => (mb / 1024).toFixed(mb % 1024 ? 1 : 0);
  const n = x => x == null ? '' : Number(x).toLocaleString();
  const pct = x => Math.round(x * 100) + '%';
  const secs = C.fmtSecs;
  const svgSrc = c => !c || !c.svg ? '' : c.svg.startsWith('<') ? 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(c.svg) : c.svg;
  const badge = (text, cls) => h`<span class="badge ${cls || ''}">${raw(cls ? '<span class="dot"></span>' : '')}${text}</span>`;
  const statusCls = s => s === 'ok' ? 'ok' : s === 'stockout' ? 'warn' : s === 'quota' ? 'serious' : s === 'error' ? 'bad' : '';

  let manifest, types, captures;
  const isRound = l => l.startsWith('round/');
  const allLabels = () => manifest.databases.map(d => d.label);
  const labels = () => allLabels().filter(l => !isRound(l));
  const rounds = () => allLabels().filter(isRound);
  const roundName = l => { const i = rounds().indexOf(l); const ts = l.slice(6); return 'study round ' + (i + 1) + ' (' + ts.slice(0, 4) + '-' + ts.slice(4, 6) + '-' + ts.slice(6, 8) + ' ' + ts.slice(9, 11) + ':' + ts.slice(11, 13) + ' UTC)'; };
  const dbName = l => isRound(l) ? roundName(l) : l;
  // Rows for listing databases: the study's rounds folded into one.
  const dbRows = () => { const rows = manifest.databases.filter(d => !isRound(d.label)).map(d => Object.assign({ link: '#/topologies?db=' + encodeURIComponent(d.label) }, d)); const rs = manifest.databases.filter(d => isRound(d.label)); if (rs.length) rows.push({ label: 'consistency study', dir: rs[0].dir.replace(/\/rounds\/[^/]+$/, '') + ' (' + rs.length + ' rounds)', machine_types: d3.max(rs, d => d.machine_types), captures: d3.sum(rs, d => d.captures), link: '#/consistency' }); return rows; };
  const studyCaptures = name => rounds().map(l => cap(l, name)).filter(Boolean);
  const cap = (l, name) => captures[l] && captures[l][name];
  const families = () => [...new Set(types.map(t => t.family))].sort();
  const regions = () => [...new Set(types.flatMap(t => t.regions))].sort();

  async function init() {
    [manifest, types, captures] = await Promise.all([load('data/manifest.json'), load('data/machine-types.json'), load('data/captures.json')]);
    document.getElementById('side-info').textContent = `${types.length} machine types · ${dbRows().length} database${dbRows().length === 1 ? '' : 's'} · exported ${manifest.generated.slice(0, 10)}`;
    document.getElementById('theme').onclick = () => { const r = document.documentElement; const dark = r.dataset.theme === 'dark' || (!r.dataset.theme && window.matchMedia('(prefers-color-scheme: dark)').matches); r.dataset.theme = dark ? 'light' : 'dark'; route(); };
    window.addEventListener('hashchange', route);
    route();
  }

  function parseHash() {
    const hash = location.hash.slice(1) || '/overview';
    const [path, query] = hash.split('?');
    const q = {}; new URLSearchParams(query || '').forEach((v, k) => { q[k] = v; });
    return { path, parts: path.split('/').filter(Boolean), q };
  }
  function setQuery(q) {
    const { path } = parseHash();
    const qs = new URLSearchParams();
    Object.entries(q).forEach(([k, v]) => { if (v !== '' && v != null && v !== false) qs.set(k, v); });
    const s = qs.toString();
    history.replaceState(null, '', '#' + path + (s ? '?' + s : ''));
  }
  function bindFilters(form, q, extra) {
    const apply = () => { const next = Object.assign({}, extra || {}); new FormData(form).forEach((v, k) => { next[k] = v === 'on' ? '1' : v; }); form.querySelectorAll('input[type=checkbox]').forEach(c => { if (!c.checked) delete next[c.name]; }); setQuery(next); route(); };
    form.addEventListener('input', debounce(apply, 200));
    form.addEventListener('change', apply);
    const clear = form.querySelector('[data-clear]'); if (clear) clear.onclick = () => { setQuery({}); route(); };
  }

  async function route() {
    const { parts, q } = parseHash();
    const view = parts[0] || 'overview';
    document.querySelectorAll('.side nav a').forEach(a => a.classList.toggle('active', a.getAttribute('href') === '#/' + view));
    C.hideTip();
    main.innerHTML = '<p class="muted">Loading…</p>';
    try {
      if (view === 'types' && parts[1]) await typeDetail(decodeURIComponent(parts[1]), q);
      else if (view === 'types') typesView(q);
      else if (view === 'topologies') topologiesView(q);
      else if (view === 'graphs') await graphsView(q);
      else if (view === 'compare') compareView(q);
      else if (view === 'experiments') await experimentsView(parts[1], q);
      else if (view === 'consistency') await consistencyView(q);
      else overview();
    } catch (err) { main.innerHTML = h`<p class="badge bad">${err.message}</p>`; console.error(err); }
    window.scrollTo(0, 0);
  }

  // ---------- overview ----------
  function overview() {
    const capturedAny = types.filter(t => (t.captured || []).length).length;
    const famCounts = d3.rollups(types, v => v.length, t => t.family).map(([family, count]) => ({ family, count }));
    const exps = manifest.experiments.map(e => h`<li><a href="#/experiments/${e.name}">${e.name}</a> <span class="muted">(${e.arms.join(', ')})</span></li>`).join('');
    main.innerHTML = h`
      <h1>Cloud topology</h1>
      <p class="lede">Machine types, captured hardware topologies and the Fluxion resource graphs built from them, across ${dbRows().length} database${dbRows().length === 1 ? '' : 's'}${manifest.quota ? ', with graphs sized by the quota of project ' + manifest.quota : ''}.</p>
      <div class="tiles">
        <div class="tile"><div class="v">${n(types.length)}</div><div class="l">machine types</div><div class="d">${families().length} families, ${regions().length} region${regions().length === 1 ? '' : 's'}</div></div>
        <div class="tile"><div class="v">${n(capturedAny)}</div><div class="l">captured in any database</div><div class="d">${pct(types.length ? capturedAny / types.length : 0)} of machine types</div></div>
        <div class="tile"><div class="v">${n(dbRows().length)}</div><div class="l">databases</div><div class="d">${n(d3.sum(manifest.databases, d => d.captures))} captures in all${raw(manifest.consistency ? ' · <a href="#/consistency">consistency</a>' : '')}</div></div>
        <div class="tile"><div class="v">${n(manifest.graphs.length)}</div><div class="l">resource graphs</div><div class="d">${manifest.levels.join(', ')}</div></div>
      </div>
      <div class="grid two">
        <div class="card"><h3>Captures per database</h3><div class="chart" id="c-db"></div><table class="small"><tr><th>Database</th><th>Directory</th><th class="num">Machine types</th><th class="num">Captures</th></tr>${raw(dbRows().map(d => h`<tr><td><a href="${d.link}">${d.label}</a></td><td class="muted">${d.dir}</td><td class="num">${n(d.machine_types)}</td><td class="num">${n(d.captures)}</td></tr>`).join(''))}</table></div>
        <div class="card"><h3>Machine types per family</h3><p class="muted small">Click a bar to list the family.</p><div class="chart" id="c-fam"></div></div>
      </div>
      <div class="grid two" style="margin-top:.9rem">
        <div class="card"><h3>Resource graphs</h3>${raw(manifest.graphs.length ? '<table class="small"><tr><th>Database</th><th>Level</th><th>Region</th><th class="num">Vertices</th></tr>' + manifest.graphs.map(g => h`<tr><td>${g.database}</td><td><a href="#/graphs?database=${g.database}&level=${g.level}&region=${g.region}">${g.level}</a></td><td>${g.region}</td><td class="num">${n(g.vertices)}</td></tr>`).join('') + '</table>' : '<p class="empty">No graphs were exported.</p>')}</div>
        <div class="card"><h3>Experiments</h3>${raw(exps ? '<ul>' + exps + '</ul>' : '<p class="empty">None exported. Pass EXPERIMENTS=… to make site.</p>')}
          <h3>Reading this site</h3><p class="small secondary">A <b>database</b> is a directory of machine types and captures: <code>data/</code>, or one arm of an experiment. A <b>capture</b> is hwloc's reading of a real instance, drawn here from its object tree. <b>Resource graphs</b> are what Fluxion schedules on: <i>series</i> pools a machine series' vCPUs, GPUs and memory as totals, <i>coarse</i> has one pooled node per machine type, and <i>exact</i> places cores, GPUs and NICs on sockets from the captures.</p></div>
      </div>`;
    C.bars(document.getElementById('c-db'), dbRows(), { label: d => d.label, value: d => d.captures, valueLabel: 'captures', onClick: d => { location.hash = d.link; } });
    C.bars(document.getElementById('c-fam'), famCounts, { label: d => d.family, value: d => d.count, valueLabel: 'machine types', max: 16, onClick: d => { location.hash = '#/types?family=' + d.family; } });
  }

  // ---------- machine types ----------
  function filterTypes(q) {
    const text = (q.q || '').toLowerCase();
    return types.filter(t =>
      (!text || t.name.includes(text) || t.series.includes(text) || (t.description || '').toLowerCase().includes(text)) &&
      (!q.family || t.family === q.family) && (!q.region || t.regions.includes(q.region)) && (!q.zone || t.zones.includes(q.zone)) &&
      (!q.min_vcpus || t.vcpus >= +q.min_vcpus) && (!q.max_vcpus || t.vcpus <= +q.max_vcpus) && (!q.min_gpus || t.gpus >= +q.min_gpus) &&
      (!q.captured || (q.captured === 'any' ? (t.captured || []).length > 0 : q.captured === 'study' ? (t.captured || []).some(isRound) : (t.captured || []).includes(q.captured))) &&
      (!q.in || t.in.includes(q.in)) && (!q.no_metal || !t.metal) && (!q.lssd || t.local_ssd) && (!q.no_deprecated || !t.deprecated));
  }
  function sortTypes(list, key, asc) {
    const val = t => key === 'memory' ? t.memory_mb : key === 'zones' ? t.zones.length : key === 'captured' ? (t.captured || []).length : key === 'accel' ? ((t.accelerators || [])[0] || {}).type || '' : t[key];
    return list.slice().sort((a, b) => { const x = val(a), y = val(b); return (x < y ? -1 : x > y ? 1 : a.name < b.name ? -1 : 1) * (asc ? 1 : -1); });
  }
  const opts = (list, sel, all) => h`<option value="">${all}</option>` + list.map(v => h`<option value="${v}" ${raw(v === sel ? 'selected' : '')}>${v}</option>`).join('');
  function typesView(q) {
    const key = q.sort || 'name', asc = q.dir !== 'desc';
    const list = sortTypes(filterTypes(q), key, asc);
    const th = (k, label, cls) => h`<th class="${(cls || '') + (key === k ? ' sorted' + (asc ? ' asc' : '') : '')}" data-sort="${k}">${label}</th>`;
    main.innerHTML = h`
      <h1>Machine types</h1>
      <p class="lede">Every machine type in the loaded databases. Filters scope the chart and the table together; a highlighted family stands out in the chart, the rest recede.</p>
      <form class="filters" id="filters">
        <label>Search<input type="search" name="q" value="${q.q || ''}" placeholder="name, series, description"></label>
        <label>Family<select name="family">${raw(opts(families(), q.family, 'all'))}</select></label>
        <label>Region<select name="region">${raw(opts(regions(), q.region, 'all'))}</select></label>
        <label>Zone<input type="text" name="zone" value="${q.zone || ''}" placeholder="us-central1-a" style="width:9rem"></label>
        <label>vCPUs ≥<input type="number" name="min_vcpus" value="${q.min_vcpus || ''}" min="0"></label>
        <label>vCPUs ≤<input type="number" name="max_vcpus" value="${q.max_vcpus || ''}" min="0"></label>
        <label>GPUs ≥<input type="number" name="min_gpus" value="${q.min_gpus || ''}" min="0"></label>
        <label>Captured in<select name="captured">${raw(opts(['any', ...labels(), ...(rounds().length ? ['study'] : [])], q.captured, 'any or none'))}</select></label>
        <label>Highlight family<select name="hi">${raw(opts(families(), q.hi, q.family ? q.family : 'none'))}</select></label>
        <label class="check"><input type="checkbox" name="no_deprecated" ${raw(q.no_deprecated ? 'checked' : '')}>hide deprecated</label>
        <label class="check"><input type="checkbox" name="no_metal" ${raw(q.no_metal ? 'checked' : '')}>hide bare metal</label>
        <button type="button" data-clear>Clear</button>
      </form>
      <div class="card"><div class="row-flex"><h3 style="margin:0">vCPUs and memory</h3><span class="muted small">${list.length} of ${types.length} machine types · log scales · hover for the nearest point, click to open</span></div>
        <div class="legend"><span><span class="sw" style="background:${C.slot(1)}"></span>${q.hi || q.family ? 'family ' + (q.hi || q.family) : 'machine type'}</span>${raw(q.hi || q.family ? '<span><span class="sw" style="background:' + C.token('--deemph') + '"></span>other families</span>' : '')}<span><span class="dia" style="background:${C.token('--text-secondary')}"></span>has GPUs</span><span><span class="sw" style="border-radius:50%;background:${C.token('--text-secondary')}"></span>CPU only</span></div>
        <div class="chart" id="c-scatter"></div></div>
      <h2>${list.length} machine types</h2>
      <div class="table-wrap"><table><thead><tr>${raw(th('name', 'Machine type'))}${raw(th('series', 'Series'))}${raw(th('vcpus', 'vCPUs', 'num'))}${raw(th('memory', 'Memory GB', 'num'))}${raw(th('gpus', 'GPUs', 'num'))}${raw(th('accel', 'Accelerator'))}${raw(th('zones', 'Zones', 'num'))}${raw(th('captured', 'Captured in'))}</tr></thead>
      <tbody>${raw(list.map(t => h`<tr class="row" data-name="${t.name}"><td><a href="#/types/${encodeURIComponent(t.name)}">${t.name}</a>${raw(t.deprecated ? ' ' + badge(t.deprecated.toLowerCase(), 'warn') : '')}${raw(t.metal ? ' ' + badge('metal') : '')}${raw(t.local_ssd ? ' ' + badge('lssd') : '')}${raw(t.shared_cpu ? ' ' + badge('shared core') : '')}</td><td>${t.series}</td><td class="num">${t.vcpus}</td><td class="num">${gb(t.memory_mb)}</td><td class="num">${t.gpus || ''}</td><td class="small">${(t.accelerators || []).map(a => a.type).join(', ')}</td><td class="num" title="${t.zones.join(', ')}">${t.zones.length}</td><td>${raw((t.captured || []).filter(l => !isRound(l)).map(l => badge(l, 'ok')).join('') + (rounds().length && (t.captured || []).some(isRound) ? badge('study ' + (t.captured || []).filter(isRound).length + '/' + rounds().length, 'ok') : ''))}</td></tr>`).join(''))}</tbody></table></div>`;
    const hi = q.hi || q.family;
    const scatterPts = (hi ? types.filter(t => filterTypes(Object.assign({}, q, { family: '' })).includes(t) || t.family === hi) : list);
    C.scatter(document.getElementById('c-scatter'), hi ? list.concat(types.filter(t => t.family !== hi && !list.includes(t) && filterTypes(Object.assign({}, q, { family: '', hi: '' })).includes(t))) : list, {
      x: t => t.vcpus, y: t => t.memory_mb / 1024, xLabel: 'vCPUs', yLabel: 'memory GB', name: t => t.name,
      isAlt: t => t.gpus > 0, emphasis: hi ? (t => t.family === hi) : null,
      extra: t => [{ k: 'series', v: t.series }].concat(t.gpus ? [{ k: 'GPUs', v: t.gpus + ' × ' + ((t.accelerators || [])[0] || {}).type }] : []).concat((t.captured || []).length ? [{ k: 'captured in', v: t.captured.join(', ') }] : []),
      onClick: t => { location.hash = '#/types/' + encodeURIComponent(t.name); },
      onHover: t => { main.querySelectorAll('tr.hi').forEach(r => r.classList.remove('hi')); if (t) { const r = main.querySelector(`tr[data-name="${CSS.escape(t.name)}"]`); if (r) r.classList.add('hi'); } },
    });
    void scatterPts;
    bindFilters(document.getElementById('filters'), q, { sort: q.sort, dir: q.dir });
    main.querySelectorAll('th[data-sort]').forEach(el => el.onclick = () => { const k = el.dataset.sort; setQuery(Object.assign({}, q, { sort: k, dir: key === k && asc ? 'desc' : 'asc' })); route(); });
    main.querySelectorAll('tr.row').forEach(tr => tr.onclick = e => { if (e.target.tagName !== 'A') location.hash = '#/types/' + encodeURIComponent(tr.dataset.name); });
  }

  function summaryTable(s) {
    if (!s) return '<p class="muted small">No hwloc summary.</p>';
    const rows = s.sockets.map(k => h`<tr><td>socket ${k.index}</td><td class="num">${k.cores}</td><td class="num">${k.pus}</td><td class="num">${k.memory_gb}</td><td class="small">${(k.gpus || []).map(d => d.name).join(', ')}</td><td class="small">${(k.nics || []).map(d => d.name).join(', ')}</td></tr>`).join('');
    return h`<table class="small"><tr><th></th><th class="num">Cores</th><th class="num">PUs</th><th class="num">Memory GB</th><th>GPUs</th><th>NICs</th></tr>${raw(rows)}<tr><td><b>total</b></td><td class="num"><b>${s.cores}</b></td><td class="num"><b>${s.pus}</b></td><td class="num"><b>${s.memory_gb}</b></td><td class="small">${(s.gpus || []).length || ''}</td><td class="small">${(s.nics || []).length || ''}</td></tr></table>`;
  }
  function agreement(name) {
    const hashes = allLabels().map(l => cap(l, name) && cap(l, name).hash).filter(Boolean);
    if (hashes.length < 2) return '';
    const same = new Set(hashes).size === 1;
    return badge(same ? 'same topology in all ' + hashes.length + ' captures' : new Set(hashes).size + ' topologies across ' + hashes.length + ' captures', same ? 'ok' : 'bad');
  }
  // One capture: an interactive topology with an image alternative, details and the summary.
  function captureCard(label, name, c, id) {
    const img = svgSrc(c);
    return h`<div class="card capture" id="${id}"><div class="row-flex" style="justify-content:space-between"><h3 style="margin:0">${label}</h3>
      <span class="tabs"><button type="button" data-tab="tree" class="on">topology</button><button type="button" data-tab="image" ${raw(img ? '' : 'disabled')}>lstopo image</button><button type="button" data-tab="summary">summary</button></span></div>
      <p class="small muted">${c.captured_at} · ${c.zone} · ${c.provisioning_model || ''} · provisioned in ${secs(c.provision_seconds)} · hwloc ${c.hwloc_version || ''} · shape <code>${c.hash || '?'}</code></p>
      <div data-pane="tree">${raw(c.hwloc ? '<div class="icicle-host"></div><p class="small muted">Width is the share of processing units below each object. Click an object to zoom in, the breadcrumb to zoom out, hover for details.</p>' : '<p class="muted small">No hwloc tree.</p>')}</div>
      <div data-pane="image" hidden>${raw(img ? '<img class="thumb" src="' + img + '" alt="lstopo rendering of ' + esc(name) + '" data-zoom>' : '')}</div>
      <div data-pane="summary" hidden>${raw(summaryTable(c.summary))}</div></div>`;
  }
  function mountCaptureCards() {
    main.querySelectorAll('.capture').forEach(card => {
      card.querySelectorAll('[data-tab]').forEach(b => b.onclick = () => { card.querySelectorAll('[data-tab]').forEach(x => x.classList.toggle('on', x === b)); card.querySelectorAll('[data-pane]').forEach(p => { p.hidden = p.dataset.pane !== b.dataset.tab; }); });
    });
    zoomable();
  }
  async function typeDetail(name) {
    const t = types.find(x => x.name === name);
    if (!t) { main.innerHTML = h`<p class="empty">No machine type ${name}.</p>`; return; }
    t.in = t.in.filter(l => !isRound(l)).concat(t.in.some(isRound) ? ['consistency study'] : []);
    const have = labels().filter(l => cap(l, name));
    const sc = studyCaptures(name), sShapes = new Set(sc.map(c => c.hash));
    const study = rounds().length ? h`<p>${raw(badge('consistency study: captured in ' + sc.length + ' of ' + rounds().length + ' rounds' + (sc.length > 1 ? ', ' + (sShapes.size === 1 ? 'the same shape every time' : sShapes.size + ' different shapes') : ''), sc.length && sShapes.size > 1 ? 'warn' : sc.length ? 'ok' : ''))} <a href="#/consistency?show=all">see consistency</a></p>` : '';
    const siblings = types.filter(x => x.series === t.series && x.name !== name).sort((a, b) => a.vcpus - b.vcpus).map(x => h`<a href="#/types/${encodeURIComponent(x.name)}">${x.name}</a>`).join('');
    main.innerHTML = h`
      <p><a href="#/types">← Machine types</a></p>
      <h1>${t.name} ${raw(agreement(name))}</h1>
      <p class="lede">${t.description || ''}</p>
      <div class="grid three">
        <div class="card"><h3>Specification</h3><dl class="kv"><dt>Family</dt><dd><a href="#/types?family=${t.family}">${t.family}</a></dd><dt>Series</dt><dd><a href="#/types?q=${t.series}">${t.series}</a></dd><dt>vCPUs</dt><dd>${t.vcpus}${t.shared_cpu ? ' (shared core)' : ''}</dd><dt>Memory</dt><dd>${gb(t.memory_mb)} GB</dd><dt>Accelerators</dt><dd>${(t.accelerators || []).map(a => a.count + ' × ' + a.type).join(', ') || 'none'}</dd><dt>Architecture</dt><dd>${t.architecture || ''}</dd><dt>Local SSD</dt><dd>${t.local_ssd ? (t.local_ssd_partitions || '') + ' ' + (t.local_ssd_interface || '') : 'no'}</dd><dt>Bare metal</dt><dd>${t.metal ? 'yes' : 'no'}</dd><dt>Deprecated</dt><dd>${t.deprecated || 'no'}</dd></dl></div>
        <div class="card"><h3>Where</h3><dl class="kv"><dt>Regions</dt><dd>${t.regions.join(', ')}</dd><dt>Zones</dt><dd class="small">${t.zones.join(', ')}</dd><dt>In databases</dt><dd>${t.in.join(', ')}</dd><dt>Captured in</dt><dd>${raw(have.map(l => badge(l, 'ok')).join('') + (sc.length ? badge('study ' + sc.length + '/' + rounds().length, 'ok') : '') || '<span class="muted">not captured</span>')}</dd></dl>
          <p class="small">In the graphs: <a href="#/graphs?level=exact&q=${t.name}">exact</a> · <a href="#/graphs?level=coarse&q=${t.name}">coarse</a> · <a href="#/graphs?level=series&q=${t.series}">series</a></p></div>
        <div class="card"><h3>Same series</h3><p class="small pill-list">${raw(siblings || '<span class="muted">none</span>')}</p></div>
      </div>
      <h2>Captures</h2>
      ${raw(study)}
      ${raw(have.length ? '<div class="grid two">' + have.map((l, i) => captureCard(l, name, cap(l, name), 'cap' + i)).join('') + '</div>' : '<p class="empty">No capture in any database. The exact graph derives this layout from a larger capture in the same series, or from the catalog.</p>')}`;
    have.forEach((l, i) => { const c = cap(l, name); const host = document.querySelector('#cap' + i + ' .icicle-host'); if (host && c.hwloc) C.icicle(host, c.hwloc, { height: icicleHeight(c) }); });
    mountCaptureCards();
  }

  // ---------- topologies ----------
  function topologiesView(q) {
    const withCaps = labels().filter(l => Object.keys(captures[l] || {}).length);
    const db = q.db && captures[q.db] ? q.db : (withCaps[0] || labels()[0]);
    const names = Object.keys(captures[db] || {}).sort((a, b) => a.localeCompare(b));
    const text = (q.q || '').toLowerCase();
    const shown = names.filter(x => !text || x.includes(text));
    const sel = q.mt && captures[db][q.mt] ? q.mt : shown[0];
    const c = sel && captures[db][sel];
    const t = sel && types.find(x => x.name === sel);
    const others = sel ? labels().filter(l => l !== db && cap(l, sel)) : [];
    main.innerHTML = h`
      <h1>Captured topologies</h1>
      <p class="lede">Each capture is hwloc's view of one real instance. The explorer draws the object tree: packages, NUMA memory, caches, cores and processing units, and the PCI devices attached to each.</p>
      <form class="filters" id="filters">
        <label>Database<select name="db">${raw(labels().map(l => h`<option value="${l}" ${raw(l === db ? 'selected' : '')}>${l} (${Object.keys(captures[l] || {}).length})</option>`).join(''))}</select></label>
        <label>Filter<input type="search" name="q" value="${q.q || ''}" placeholder="machine type"></label>
        <label>Machine type<select name="mt">${raw(shown.map(x => h`<option value="${x}" ${raw(x === sel ? 'selected' : '')}>${x}</option>`).join(''))}</select></label>
      </form>
      ${raw(c && c.hwloc ? (() => { const st = treeStats(c.hwloc); return h`<div class="tiles"><div class="tile"><div class="v">${n(st.vertices)}</div><div class="l">vertices</div><div class="d">hwloc objects in ${sel}</div></div><div class="tile"><div class="v">${n(st.edges)}</div><div class="l">edges</div><div class="d">parent to child</div></div>${raw(Object.entries(st.byType).sort((a, b) => b[1] - a[1]).slice(0, 4).map(([k, v]) => h`<div class="tile"><div class="v">${n(v)}</div><div class="l">${k} objects</div></div>`).join(''))}</div>`; })() : '')}
      ${raw(c ? h`<div class="card"><div class="row-flex" style="justify-content:space-between"><div><h2 style="margin:0"><a href="#/types/${encodeURIComponent(sel)}">${sel}</a> <span class="muted small">in ${db}</span></h2><p class="small muted" style="margin:.2rem 0 0">${t ? t.vcpus + ' vCPUs, ' + gb(t.memory_mb) + ' GB' + (t.gpus ? ', ' + t.gpus + ' GPUs' : '') + ' · ' : ''}captured ${c.captured_at} in ${c.zone} (${c.provisioning_model || ''}) · ${c.summary ? c.summary.sockets.length + ' socket' + (c.summary.sockets.length === 1 ? '' : 's') + ', ' + c.summary.cores + ' cores, ' + c.summary.pus + ' PUs' : ''}</p></div>
        <span class="tabs"><button type="button" data-tab="tree" class="on">topology</button><button type="button" data-tab="image" ${raw(c.svg ? '' : 'disabled')}>lstopo image</button><button type="button" data-tab="summary">summary</button></span></div>
        <div class="capture"><div data-pane="tree"><div class="icicle-host" id="explorer"></div><p class="small muted">Width is the share of processing units below each object. Click an object to zoom in, the breadcrumb to zoom out, hover for details.</p></div>
        <div data-pane="image" hidden>${raw(c.svg ? '<img class="thumb" src="' + svgSrc(c) + '" alt="" data-zoom>' : '')}</div>
        <div data-pane="summary" hidden>${raw(summaryTable(c.summary))}</div></div>
        ${raw(others.length ? '<p class="small">Also captured in ' + others.map(l => h`<a href="#/topologies?db=${l}&mt=${sel}">${l}</a>`).join(', ') + '. ' + agreement(sel) + '</p>' : '')}</div>` : '<p class="empty">No captures in this database.</p>')}
      <h2>${shown.length} captures in ${db}</h2>
      <div class="cards">${raw(shown.map(x => { const k = captures[db][x]; return h`<div class="card"><h3><a href="#/topologies?db=${db}&mt=${x}">${x}</a></h3><p class="small muted">${k.captured_at.slice(0, 10)} · ${k.zone} · ${k.provisioning_model || ''}${k.summary ? ' · ' + k.summary.sockets.length + ' socket' + (k.summary.sockets.length === 1 ? '' : 's') + ', ' + k.summary.cores + ' cores, ' + k.summary.pus + ' PUs' + ((k.summary.gpus || []).length ? ', ' + k.summary.gpus.length + ' GPUs' : '') : ''}</p><div class="chart mini" data-mini="${x}"></div></div>`; }).join(''))}</div>`;
    if (c && c.hwloc) C.icicle(document.getElementById('explorer'), c.hwloc, { height: icicleHeight(c, 480) });
    const host = main.querySelector('.capture'); if (host) { const card = host.parentElement; card.querySelectorAll('[data-tab]').forEach(b => b.onclick = () => { card.querySelectorAll('[data-tab]').forEach(x => x.classList.toggle('on', x === b)); card.querySelectorAll('[data-pane]').forEach(p => { p.hidden = p.dataset.pane !== b.dataset.tab; }); }); }
    // Small static icicles for the gallery, drawn lazily as they scroll into view.
    const draw = el => { const k = captures[db][el.dataset.mini]; if (k && k.hwloc && !el.dataset.done) { el.dataset.done = 1; miniIcicle(el, k.hwloc); } };
    const minis = [...main.querySelectorAll('[data-mini]')];
    if (window.IntersectionObserver) { const io = new IntersectionObserver(es => es.forEach(e => { if (e.isIntersecting) { draw(e.target); io.unobserve(e.target); } })); minis.forEach(m => io.observe(m)); } else minis.forEach(draw);
    bindFilters(document.getElementById('filters'), q);
    zoomable();
  }
  function treeStats(root) { const st = { vertices: 0, edges: 0, byType: {} }; (function walk(o) { st.vertices++; st.byType[o.t] = (st.byType[o.t] || 0) + 1; (o.c || []).forEach(k => { st.edges++; walk(k); }); })(root); return st; }
  const icicleHeight = (c, base) => Math.min(900, Math.max(base || 320, 3 * ((c.summary && c.summary.pus) || 0)));
  function miniIcicle(el, hwloc) {
    const root = d3.hierarchy(hwloc, d => d.c).sum(d => d.c && d.c.length ? 0 : 1);
    const W = el.clientWidth || 300, H = 64;
    d3.partition().size([W, H]).padding(0.5)(root);
    const svg = d3.select(el).append('svg').attr('viewBox', [0, 0, W, H]).attr('width', W).attr('height', H);
    svg.selectAll('rect').data(root.descendants().filter(d => d.depth < 7 && d.x1 - d.x0 > 0.6)).join('rect').attr('x', d => d.x0).attr('y', d => d.y0).attr('width', d => d.x1 - d.x0).attr('height', d => Math.max(0, d.y1 - d.y0)).attr('fill', d => ({ Package: C.slot(1), NUMANode: C.slot(3), Core: C.slot(7), PU: C.slot(7), PCIDev: C.slot(2), OSDev: C.slot(2) }[d.data.t] || (d.data.t.endsWith('Cache') ? C.slot(4) : C.token('--deemph')))).attr('fill-opacity', d => d.data.t === 'PU' ? 0.55 : d.data.t.endsWith('Cache') ? 0.6 : 0.9);
  }

  // ---------- graphs ----------
  async function graphsView(q) {
    const graphs = manifest.graphs;
    if (!graphs.length) { main.innerHTML = '<h1>Resource graphs</h1><p class="empty">No graphs were exported.</p>'; return; }
    const info = graphs.find(g => g.database === (q.database || graphs[0].database) && g.level === (q.level || 'exact') && g.region === (q.region || graphs[0].region)) || graphs.find(g => g.level === (q.level || 'exact')) || graphs[0];
    const tree = await load(info.file);
    const view = q.view || 'tree';
    const pick = (k, vals, sel) => h`<label>${k[0].toUpperCase() + k.slice(1)}<select name="${k}">${raw(vals.map(v => h`<option value="${v}" ${raw(v === sel ? 'selected' : '')}>${v}</option>`).join(''))}</select></label>`;
    const counts = {}; (function walk(t, mult) { const k = mult * t.count; counts[t.type] = (counts[t.type] || 0) + k; (t.children || []).forEach(c => walk(c, k)); })(tree, 1);
    main.innerHTML = h`
      <h1>Resource graphs</h1>
      <p class="lede">The Fluxion graph as a tree: cluster, region, zones, offers, nodes, sockets, cores. Siblings with the same shape are folded into one entry with a count, so a 96-core node is one core entry ×96.</p>
      <form class="filters" id="filters">
        ${raw(pick('database', [...new Set(graphs.map(g => g.database))], info.database))}
        ${raw(pick('level', [...new Set(graphs.map(g => g.level))], info.level))}
        ${raw(pick('region', [...new Set(graphs.map(g => g.region))], info.region))}
        <label>Find<input type="search" name="q" value="${q.q || ''}" placeholder="offer, machine type, property"></label>
        <label>View<span class="tabs"><button type="button" data-view="tree" class="${view === 'tree' ? 'on' : ''}">tree</button><button type="button" data-view="list" class="${view === 'list' ? 'on' : ''}">list</button></span></label>
        <button type="button" id="expand">Expand all</button><button type="button" id="collapse">Collapse</button>${raw(view === 'tree' ? '<button type="button" id="recenter">Recenter</button>' : '')}
      </form>
      <div class="tiles"><div class="tile"><div class="v">${n(info.vertices)}</div><div class="l">vertices</div><div class="d">${info.level} graph of ${info.region}, ${info.database}</div></div><div class="tile"><div class="v">${n(info.edges != null ? info.edges : info.vertices - 1)}</div><div class="l">edges</div><div class="d">containment</div></div></div>
      <div class="tiles">${raw(['offer', 'node', 'socket', 'core', 'gpu', 'nic', 'memory', 'local_ssd'].filter(k => counts[k]).map(k => h`<div class="tile"><div class="v">${n(counts[k])}</div><div class="l">${k} vertices</div></div>`).join(''))}</div>
      ${raw(view === 'tree' ? '<div class="gtree-wrap"><div class="chart" id="gtree"></div><div class="card" id="gsel"><h3>Selected vertex</h3><p class="muted small">Click a vertex to see its properties here. Filled circles have hidden children; drag to pan, scroll to zoom.</p></div></div>' : '<div class="card list-tree" id="glist"></div>')}`;
    const q2 = (q.q || '').toLowerCase();
    let api;
    if (view === 'tree') {
      api = C.graphTree(document.getElementById('gtree'), tree, { depth: q2 ? 1 : (info.level === 'series' ? 3 : 2), query: q2, onSelect: d => {
        const el = document.getElementById('gsel');
        const props = Object.entries(d.data.properties || {}).map(([k, v]) => h`<dt>${k}</dt><dd>${v}</dd>`).join('');
        el.innerHTML = h`<h3>${d.data.type} ${d.data.name}${d.data.count > 1 ? ' ×' + n(d.data.count) : ''}</h3><dl class="kv"><dt>vertices</dt><dd>${n(d.data.vertices)}</dd>${raw(d.data.count > 1 ? h`<dt>folded</dt><dd>${d.data.name} … ${d.data.last}</dd>` : '')}${raw(d.data.size ? h`<dt>size</dt><dd>${d.data.size} ${d.data.unit || ''}</dd>` : '')}${raw(d.data.exclusive ? '<dt>exclusive</dt><dd>yes</dd>' : '')}${raw(props)}</dl>${raw(d.data.properties && d.data.properties.machine_type ? h`<p class="small"><a href="#/types/${encodeURIComponent(d.data.properties.machine_type)}">machine type ${d.data.properties.machine_type}</a></p>` : '')}`;
      } });
      document.getElementById('recenter').onclick = () => api.reset();
    } else {
      const root = document.getElementById('glist'); root.appendChild(renderList(tree, q2, 0));
      api = { expandAll: () => { let more = true, rounds = 0; while (more && rounds++ < 30) { more = false; root.querySelectorAll('details:not([open])').forEach(d => { d.open = true; more = true; }); } }, collapseAll: () => root.querySelectorAll('details').forEach(d => { d.open = false; }) };
    }
    document.getElementById('expand').onclick = () => api.expandAll();
    document.getElementById('collapse').onclick = () => api.collapseAll();
    const form = document.getElementById('filters');
    form.querySelectorAll('[data-view]').forEach(b => b.onclick = () => { setQuery(Object.assign({}, q, { view: b.dataset.view })); route(); });
    bindFilters(form, q, { view });
  }
  const lmatch = (t, text) => text && (t.name.toLowerCase().includes(text) || t.type.includes(text) || Object.entries(t.properties || {}).some(([k, v]) => (k + '=' + v).toLowerCase().includes(text)));
  function lhas(t, text) { if (!text) return false; if (t._m === undefined) t._m = lmatch(t, text) || (t.children || []).some(c => lhas(c, text)); return t._m; }
  function llabel(t) {
    const props = Object.entries(t.properties || {}).map(([k, v]) => k + '=' + v).join(' ');
    return h`<span class="type">${t.type}</span> ${t.name}${raw(t.count > 1 ? ' <span class="count">×' + n(t.count) + '</span>' : '')}${raw(t.size ? ' <span class="muted">' + t.size + (t.unit ? ' ' + esc(t.unit) : '') + '</span>' : '')}${raw(props ? ' <span class="props">' + esc(props) + '</span>' : '')}`;
  }
  function renderList(t, text, depth) {
    const hit = lmatch(t, text);
    if (!t.children || !t.children.length) { const d = document.createElement('div'); d.className = 'leaf' + (hit ? ' hit' : ''); d.innerHTML = llabel(t); return d; }
    const d = document.createElement('details'); if (hit) d.className = 'hit';
    const s = document.createElement('summary'); s.innerHTML = llabel(t) + ' <span class="muted">' + n(t.vertices) + ' vertices</span>'; d.appendChild(s);
    let done = false;
    const fill = () => { if (done) return; done = true; const kids = text ? t.children.filter(c => lhas(c, text) || lmatch(t, text)) : t.children; kids.forEach(c => d.appendChild(renderList(c, text, depth + 1))); if (text && kids.length < t.children.length) { const m = document.createElement('div'); m.className = 'leaf muted'; m.textContent = (t.children.length - kids.length) + ' entries hidden by the filter'; d.appendChild(m); } };
    d.addEventListener('toggle', () => { if (d.open) fill(); });
    if (depth < 2 || (text && lhas(t, text))) { d.open = true; fill(); }
    return d;
  }

  // ---------- compare ----------
  function compareView(q) {
    const all = labels();
    const chosen = q.db ? q.db.split(',').filter(l => all.includes(l)) : all;
    const only = q.only || 'captured';
    const rows = types.filter(t => {
      const caps = chosen.map(l => cap(l, t.name)); const k = caps.filter(Boolean).length;
      return only === 'all' ? true : only === 'captured' ? k > 0 : only === 'differ' ? (k > 1 && new Set(caps.filter(Boolean).map(c => c.hash)).size > 1) : only === 'missing' ? (k > 0 && k < chosen.length) : true;
    }).sort((a, b) => a.vcpus - b.vcpus || a.name.localeCompare(b.name));
    const verdict = t => { const hs = chosen.map(l => cap(l, t.name)).filter(Boolean).map(c => c.hash); return hs.length < 2 ? '' : new Set(hs).size === 1 ? '✓ same shape' : '✕ differs'; };
    const totals = chosen.map(l => ({ label: l, captures: rows.filter(t => cap(l, t.name)).length, mean: d3.mean(rows.map(t => cap(l, t.name)).filter(c => c && c.provision_seconds > 0), c => c.provision_seconds) }));
    main.innerHTML = h`
      <h1>Compare databases</h1>
      <p class="lede">The same machine types across databases: who captured what, how long provisioning took, and whether the captured shapes agree. A shape is the sockets, cores, PUs, memory and device names of a capture.</p>
      <form class="filters" id="filters">
        ${raw(all.map(l => h`<label class="check"><input type="checkbox" name="db" value="${l}" ${raw(chosen.includes(l) ? 'checked' : '')}>${l}</label>`).join(''))}
        <label>Show<select name="only">${raw(['captured', 'differ', 'missing', 'all'].map(v => h`<option value="${v}" ${raw(v === only ? 'selected' : '')}>${{ captured: 'captured in any', differ: 'shapes differ', missing: 'captured in some, not all', all: 'every machine type' }[v]}</option>`).join(''))}</select></label>
      </form>
      <div class="tiles">${raw(totals.map(s => h`<div class="tile"><div class="v">${s.captures}</div><div class="l">captured in ${s.label}</div><div class="d">${s.mean ? 'mean provision ' + secs(s.mean) : ''}</div></div>`).join(''))}</div>
      <div class="card"><h3>Provisioning time by machine type and database</h3><p class="muted small">${rows.length} machine types. Colors run from yellow (fast) to dark blue (slow) in fixed steps, finest around one minute where most captures fall. Empty cells were not captured. Click a row to open the machine type.</p>
        ${raw(rows.length ? '<div class="chart" id="c-heat"></div>' : '<p class="empty">Nothing to compare with this filter.</p>')}</div>
      <h2>Table</h2>
      <div class="table-wrap"><table><thead><tr><th>Machine type</th><th class="num">vCPUs</th>${raw(chosen.map(l => h`<th>${l}</th>`).join(''))}<th>Shape</th></tr></thead>
      <tbody>${raw(rows.map(t => h`<tr><td><a href="#/types/${encodeURIComponent(t.name)}">${t.name}</a></td><td class="num">${t.vcpus}</td>${raw(chosen.map(l => { const c = cap(l, t.name); return '<td>' + (c ? badge('ok', 'ok') + h` <span class="small">${secs(c.provision_seconds)} · ${c.provisioning_model || ''} · ${c.zone}</span>` : '<span class="muted">—</span>') + '</td>'; }).join(''))}<td>${raw(verdict(t) ? badge(verdict(t), verdict(t).startsWith('✓') ? 'ok' : 'bad') : '')}</td></tr>`).join(''))}</tbody></table></div>`;
    if (rows.length) C.heatmap(document.getElementById('c-heat'), rows, chosen, { value: (t, l) => { const c = cap(l, t.name); return c && c.provision_seconds > 0 ? c.provision_seconds : null; }, format: secs, rowLabel: t => t.name, colLabel: l => l, extra: verdict, tipExtra: (t, l) => { const c = cap(l, t.name); return c ? [{ k: 'zone', v: c.zone }, { k: 'model', v: c.provisioning_model || '' }, { k: 'captured', v: c.captured_at }] : []; }, onClick: t => { location.hash = '#/types/' + encodeURIComponent(t.name); } });
    const form = document.getElementById('filters');
    form.addEventListener('change', () => { const dbs = [...form.querySelectorAll('[name=db]:checked')].map(i => i.value); setQuery({ db: dbs.join(','), only: form.only.value }); route(); });
  }

  // ---------- experiments ----------
  async function experimentsView(name) {
    const exps = manifest.experiments;
    if (!exps.length) { main.innerHTML = '<h1>Experiments</h1><p class="empty">No experiments were exported. Pass EXPERIMENTS=… to make site.</p>'; return; }
    const e = exps.find(x => x.name === name) || exps[0];
    const r = await load(e.file);
    const armNames = r.arms.map(a => a.arm);
    const armDb = a => e.arms.find(l => l.endsWith('/' + a));
    const outcome = o => { if (!o) return ''; const notes = []; if (o.status === 'ok') { if (o.provision_seconds) notes.push(secs(o.provision_seconds) + ' provision'); if (o.provisioning_model) notes.push(o.provisioning_model); if (o.zone) notes.push(o.zone); } if (o.attempts > 1) notes.push(o.attempts + ' attempts'); if (o.seconds && o.status !== 'ok') notes.push(secs(o.seconds)); if (o.error && o.status !== 'ok') notes.push(o.error); return badge((C.statusIcon[o.status] || '') + ' ' + o.status, statusCls(o.status)) + h` <span class="small muted">${notes.join(' · ')}</span>`; };
    const groups = r.arms.map(a => { const counts = {}; r.rows.forEach(row => { const s = (row.outcomes[a.arm] || {}).status || 'other'; const k = ['ok', 'stockout', 'quota', 'error'].includes(s) ? s : 'other'; counts[k] = (counts[k] || 0) + 1; }); return { name: a.arm, counts }; });
    main.innerHTML = h`
      <h1>Experiment ${e.name}</h1>
      ${raw(exps.length > 1 ? '<p class="pill-list">' + exps.map(x => h`<a href="#/experiments/${x.name}">${x.name}</a>`).join(' ') + '</p>' : '')}
      <p class="lede">The same plan of ${r.planned} machine types worked by each arm from the same starting state. "Either" is what the two approaches reach together.</p>
      <div class="tiles">
        ${raw(r.arms.map(a => h`<div class="tile"><div class="v">${a.captured}<span class="muted" style="font-size:1rem">/${a.planned}</span></div><div class="l">${a.arm} captured</div><div class="d">${pct(a.rate)}${a.seconds ? ' · ' + secs(a.seconds) + ' wall clock' : ''}${a.mean_provision_seconds ? ' · mean provision ' + secs(a.mean_provision_seconds) : ''}</div></div>`).join(''))}
        <div class="tile"><div class="v">${r.either}<span class="muted" style="font-size:1rem">/${r.planned}</span></div><div class="l">either arm</div><div class="d">${pct(r.planned ? r.either / r.planned : 0)} · both ${r.both}, ${armNames.map(a => a + ' only ' + (r.only[a] || 0)).join(', ')}, neither ${r.neither}</div></div>
        ${raw(r.arms.filter(a => a.agent).map(a => h`<div class="tile"><div class="v">$${(a.agent.cost_usd || 0).toFixed(2)}</div><div class="l">${a.arm} tokens</div><div class="d">${a.agent.turns} turns · ${secs(a.agent.api_ms / 1000)} of model time · ${a.agent.attempts_used} capture attempts</div></div>`).join(''))}
      </div>
      <div class="grid two">
        <div class="card"><h3>Outcomes per arm</h3><div class="legend">${raw(['ok', 'stockout', 'quota', 'error'].map(s => h`<span><span class="sw" style="background:${C.statusColor(s)}"></span>${C.statusIcon[s]} ${s === 'ok' ? 'captured' : s}</span>`).join(''))}<span><span class="sw" style="background:${C.token('--deemph')}"></span>not captured</span></div><div class="chart" id="c-outcomes"></div></div>
        <div class="card"><h3>Provisioning time per machine type</h3><p class="muted small">Log scale, so one slow capture does not hide the rest.</p><div class="legend">${raw(armNames.map((a, i) => h`<span><span class="sw" style="border-radius:50%;background:${C.slot(i + 1)}"></span>${a}</span>`).join(''))}</div><div class="chart" id="c-dumbbell"></div></div>
      </div>
      <div class="card" style="margin-top:.9rem"><h3>Timeline</h3><p class="muted small">Each bar is one capture; both arms are drawn from their own start, so the lanes compare how far each had got at the same elapsed time. Hover for the clock time.</p><div class="chart" id="c-timeline"></div></div>
      <h2>Machine types</h2>
      <div class="table-wrap"><table><thead><tr><th>Machine type</th><th>Model</th>${raw(armNames.map(a => h`<th>${a}</th>`).join(''))}</tr></thead>
      <tbody>${raw(r.rows.map(row => h`<tr><td><a href="#/types/${encodeURIComponent(row.machine_type)}">${row.machine_type}</a></td><td>${row.model}</td>${raw(armNames.map(a => '<td>' + outcome(row.outcomes[a]) + '</td>').join(''))}</tr>`).join(''))}</tbody></table></div>
      ${raw(r.arms.filter(a => a.extra && a.extra.length).map(a => h`<p class="small">Captures outside the plan in the ${a.arm} arm: ${a.extra.join(', ')}.</p>`).join(''))}
      ${raw(e.arms.length ? '<p class="small">Browse the arms as databases: ' + e.arms.map(l => h`<a href="#/topologies?db=${l}">${l}</a>`).join(', ') + '; or <a href="#/compare?db=' + esc(e.arms.join(',')) + '">compare them</a>.</p>' : '')}`;
    C.stackedStatus(document.getElementById('c-outcomes'), groups);
    const dRows = r.rows.map(row => ({ label: row.machine_type, a: (row.outcomes[armNames[0]] || {}).provision_seconds || null, b: (row.outcomes[armNames[1]] || {}).provision_seconds || null })).filter(x => x.a || x.b);
    if (dRows.length) C.dumbbell(document.getElementById('c-dumbbell'), dRows, { aName: armNames[0], bName: armNames[1], onClick: x => { location.hash = '#/types/' + encodeURIComponent(x.label); } });
    else document.getElementById('c-dumbbell').innerHTML = '<p class="empty">No captures yet.</p>';
    // Timeline from what each arm recorded: capture end times and durations.
    // Each arm on its own clock: seconds since that arm started, so the arms line up.
    const elapsed = r.arms.every(a => a.start);
    const lanes = r.arms.map(a => { const t0 = elapsed ? new Date(a.start).getTime() : 0; return { name: a.arm, items: r.rows.map(row => { const o = row.outcomes[a.arm]; if (!o || !o.captured_at) return null; const endMs = new Date(o.captured_at).getTime(); const dur = (o.seconds || o.provision_seconds || 60) * 1000; const item = { label: row.machine_type, status: o.status, extra: [{ k: 'zone', v: o.zone || '' }, { k: 'model', v: o.provisioning_model || '' }, { k: 'captured', v: o.captured_at }] }; if (elapsed) { item.start = Math.max(0, (endMs - dur - t0) / 1000); item.end = Math.max(item.start + 1, (endMs - t0) / 1000); } else { item.start = new Date(endMs - dur); item.end = new Date(endMs); } return item; }).filter(Boolean) }; });
    if (lanes.some(l => l.items.length)) C.timeline(document.getElementById('c-timeline'), lanes, { elapsed }); else document.getElementById('c-timeline').innerHTML = '<p class="empty">No timestamps yet.</p>';
  }

  // ---------- consistency ----------
  async function consistencyView(q) {
    if (!manifest.consistency) { main.innerHTML = '<h1>Consistency</h1><p class="empty">Nothing to compare: export more than one database with captures, or a consistency study (STUDY=…).</p>'; return; }
    const r = await load(manifest.consistency);
    const show = q.show || 'varying';
    const rows = r.types.filter(t => show === 'all' ? true : show === 'varying' ? t.shapes > 1 : show === 'repeated' ? t.captures > 1 : true);
    // Shape slots per type: the most common shape is slot 1, so the odd ones stand out.
    const slots = {};
    r.types.forEach(t => { const counts = d3.rollups(t.observations, v => v.length, o => o.shape).sort((a, b) => b[1] - a[1]); counts.forEach(([id], i) => { slots[t.machine_type + ':' + id] = i + 1; }); });
    const slotOf = (mt, id) => slots[mt + ':' + id] || 1;
    const kinds = d3.rollups(r.databases, v => v.length, d => d.startsWith('round/') ? 'study rounds' : d.includes('/') ? 'experiment arms' : 'databases');
    const field = (f, name) => ({ sockets: f.sockets, cores: f.cores, pus: f.pus, numa: f.numa_nodes, memory: f.memory_gb + ' GB', l3: (f.l3_mb || []).join('/') + ' MB', l2: (f.l2_kb || []).join('/') + ' KB', 'cpu model': (f.cpu_models || []).join(', '), 'gpu placement': 'sockets [' + f.gpus.per_socket.join(' ') + '] machine ' + f.gpus.machine, 'nic placement': 'sockets [' + f.nics.per_socket.join(' ') + '] machine ' + f.nics.machine })[name];
    const split = t => d3.rollups(t.observations, v => v.length, o => o.shape).sort((a, b) => b[1] - a[1]).map(([id, n]) => h`<span class="badge clickable" data-type="${t.machine_type}" data-shape="${id}" title="show shape ${id}"><span class="dot" style="background:${C.slot(Math.min(8, slotOf(t.machine_type, id)))}"></span>${n}</span>`).join('');
    main.innerHTML = h`
      <h1>Capture consistency</h1>
      <p class="lede">Every capture of a machine type, compared on what the hardware looked like: sockets, cores, PUs, NUMA nodes, memory, cache sizes, CPU model, and where GPUs and NICs attach. The kernel and hwloc version are recorded but not compared, so a changed capture environment can be told from changed hardware.</p>
      <div class="tiles"><div class="tile"><div class="v">${r.consistent}</div><div class="l">types consistent</div><div class="d">same shape in every capture</div></div><div class="tile"><div class="v">${r.varying}</div><div class="l">types varying</div><div class="d">more than one shape</div></div><div class="tile"><div class="v">${r.single}</div><div class="l">captured once</div><div class="d">nothing to compare yet</div></div><div class="tile"><div class="v">${n(d3.sum(r.types, t => t.captures))}</div><div class="l">captures compared</div><div class="d">${kinds.map(([k, v]) => v + ' ' + k).join(', ')}</div></div></div>
      <form class="filters" id="filters"><label>Show<select name="show">${raw(['varying', 'repeated', 'all'].map(v => h`<option value="${v}" ${raw(v === show ? 'selected' : '')}>${{ varying: 'types that vary', repeated: 'types captured more than once', all: 'every captured type' }[v]}</option>`).join(''))}</select></label></form>
      <div class="card"><h3>Every capture over time</h3><div class="legend"><span><span class="sw" style="border-radius:50%;background:${C.slot(1)}"></span>the type's most common shape</span><span><span class="sw" style="border-radius:50%;background:${C.slot(2)}"></span>second shape</span><span><span class="sw" style="border-radius:50%;background:${C.slot(3)}"></span>third shape</span></div><p class="muted small">One row per machine type, one dot per capture at the time it was taken. A row of one color is a consistent type. Hover for the capture; click a dot to see that capture's topology, or a name to open the type.</p>${raw(rows.length ? '<div class="chart" id="c-strip"></div>' : '<p class="empty">No machine types match.</p>')}</div>
      <h2>${rows.length} machine types</h2>
      <div class="table-wrap"><table><thead><tr><th>Machine type</th><th class="num">Captures</th><th>Shapes</th><th>Varies in</th><th>Zones seen</th><th>CPU models seen</th></tr></thead><tbody>
      ${raw(rows.map(t => h`<tr><td><a href="#/types/${encodeURIComponent(t.machine_type)}">${t.machine_type}</a></td><td class="num">${t.captures}</td><td>${raw(split(t))}</td><td>${raw((t.varies || []).map(v => badge(v, 'warn')).join('') || '<span class="muted">nothing</span>')}</td><td class="small">${t.zones.map(z => z.replace(/^.*-/, '')).join(', ')}</td><td class="small">${[...new Set(t.observations.flatMap(o => o.fingerprint.cpu_models || []))].join(' · ') || 'not reported'}</td></tr>`).join(''))}</tbody></table></div>
      ${raw(rows.filter(t => t.shapes > 1).map(t => h`<div class="card" style="margin-top:.9rem"><h3>${t.machine_type}: ${t.varies.join(', ')}</h3><div class="table-wrap" style="max-height:24rem"><table class="small"><thead><tr><th>Captured</th><th>Database</th><th>Zone</th><th>Model</th><th>Shape</th>${raw(t.varies.map(v => h`<th>${v}</th>`).join(''))}<th>Kernel</th><th>hwloc</th></tr></thead><tbody>${raw(t.observations.map((o, i) => h`<tr class="row" data-obs="${t.machine_type}|${i}"><td>${o.captured_at}</td><td>${dbName(o.database)}</td><td>${o.zone}</td><td>${o.model}</td><td><span class="badge"><span class="dot" style="background:${C.slot(Math.min(8, slotOf(t.machine_type, o.shape)))}"></span>${o.shape}</span></td>${raw(t.varies.map(v => h`<td>${field(o.fingerprint, v)}</td>`).join(''))}<td>${o.fingerprint.kernel || ''}</td><td>${o.fingerprint.hwloc || ''}</td></tr>`).join(''))}</tbody></table></div></div>`).join(''))}`;
    const pick = (mt, o) => showCapture(mt, o, r.types.find(t => t.machine_type === mt), slotOf, field);
    main.querySelectorAll('[data-shape]').forEach(b => b.onclick = () => { const t = r.types.find(x => x.machine_type === b.dataset.type); const o = t.observations.find(x => x.shape === b.dataset.shape); pick(t.machine_type, o); });
    main.querySelectorAll('tr[data-obs]').forEach(tr => tr.onclick = () => { const [mt, i] = tr.dataset.obs.split('|'); const t = r.types.find(x => x.machine_type === mt); pick(mt, t.observations[+i]); });
    if (rows.length) C.shapeStrip(document.getElementById('c-strip'), rows, { slotOf, dbName, onPick: d => pick(d.type, d), onClick: t => { location.hash = '#/types/' + encodeURIComponent(t.machine_type); } });
    bindFilters(document.getElementById('filters'), q);
  }

  // One capture in the modal: what differs from the type's usual shape, and its topology.
  function showCapture(mt, o, t, slotOf, field) {
    const c = cap(o.database, mt);
    const common = d3.rollups(t.observations, v => v.length, x => x.shape).sort((a, b) => b[1] - a[1])[0][0];
    const base = t.observations.find(x => x.shape === common);
    const fields = ['sockets', 'cores', 'pus', 'numa', 'memory', 'l3', 'l2', 'cpu model', 'gpu placement', 'nic placement'];
    const diff = fields.filter(f => String(field(o.fingerprint, f)) !== String(field(base.fingerprint, f)));
    const body = modal.querySelector('.modal-content');
    body.innerHTML = h`<div style="width:min(1100px,88vw)"><h2 style="margin-top:0">${mt} <span class="badge"><span class="dot" style="background:${C.slot(Math.min(8, slotOf(mt, o.shape)))}"></span>shape ${o.shape}</span></h2>
      <p class="small muted">${dbName(o.database)} · captured ${o.captured_at} · ${o.zone} · ${o.model} · kernel ${o.fingerprint.kernel || '?'} · hwloc ${o.fingerprint.hwloc || '?'}</p>
      ${raw(o.shape === common ? h`<p>${raw(badge('the most common shape for this type', 'ok'))} seen in ${t.observations.filter(x => x.shape === common).length} of ${t.captures} captures.</p>` : h`<p>${raw(badge('differs from the usual shape', 'warn'))} seen in ${t.observations.filter(x => x.shape === o.shape).length} of ${t.captures} captures.</p><table class="small"><tr><th>Field</th><th>This capture</th><th>Usual shape (${common})</th></tr>${raw(diff.map(f => h`<tr><td>${f}</td><td><b>${field(o.fingerprint, f)}</b></td><td>${field(base.fingerprint, f)}</td></tr>`).join(''))}</table>`)}
      <div class="icicle-host" id="modal-icicle"></div>
      <p class="small"><a href="#/types/${encodeURIComponent(mt)}">open ${mt}</a></p></div>`;
    modal.hidden = false;
    body.querySelector('a').onclick = () => { modal.hidden = true; };
    if (c && c.hwloc) C.icicle(document.getElementById('modal-icicle'), c.hwloc, { height: Math.min(560, Math.max(300, 3 * ((c.summary && c.summary.pus) || 0))) });
    else document.getElementById('modal-icicle').innerHTML = '<p class="muted">No topology tree for this capture.</p>';
  }

  // ---------- helpers ----------
  const modal = document.getElementById('modal');
  modal.querySelector('.close').onclick = () => { modal.hidden = true; };
  modal.onclick = e => { if (e.target === modal) modal.hidden = true; };
  document.addEventListener('keydown', e => { if (e.key === 'Escape') modal.hidden = true; });
  function zoomable() { main.querySelectorAll('img[data-zoom]').forEach(img => img.onclick = () => { modal.querySelector('.modal-content').innerHTML = '<img src="' + img.src + '" alt="">'; modal.hidden = false; }); }
  function debounce(fn, ms) { let t; return () => { clearTimeout(t); t = setTimeout(fn, ms); }; }

  init().catch(err => { main.innerHTML = h`<p class="badge bad">Could not load the site data: ${err.message}. Opening index.html from disk needs a single-file export (make site SINGLE=1); otherwise serve the directory (make site-serve).</p>`; });
})();
