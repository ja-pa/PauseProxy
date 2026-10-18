
const el = id => document.getElementById(id);
const list = text => text.split(/[\n,]+/).map(s => s.trim()).filter(Boolean);
const presets = {
  x: {domain: 'x.com', domains: ['x.com', 'www.x.com'], bypass: ['twimg.com']},
  youtube: {domain: 'youtube.com', domains: ['youtube.com', 'www.youtube.com', 'm.youtube.com', 'youtu.be'], bypass: ['googlevideo.com', 'ytimg.com']}
};
let password = '', polling, editing = '', editorBaseline = '', editorReturnFocus;
let siteBusy = false, autoDomains = '';
let rules = {sites: [], groups: [], bypass_domains: []};
let editingGroup = '', groupBaseline = '';
let siteOverview = [], stateLoaded = false, stateUnavailable = false;
const remainingCells = new Map();
const draftFields = ['preset', 'domain', 'domains', 'minutes', 'always-block', 'enabled', 'site-group', 'budget-mode',
  'blur-enabled', 'blur-minutes', 'grayscale-enabled', 'grayscale-minutes', 'extra-enabled', 'extra-method', 'extra-minutes', 'extra-uses', 'preset-bypass'];
const validatedFields = ['domain', 'domains', 'minutes', 'blur-minutes', 'grayscale-minutes', 'extra-minutes', 'extra-uses', 'preset-bypass'];

function signedOut(message = '') {
  siteOverview = []; stateLoaded = false; el('sites-user').replaceChildren(); el('sites-user').value = '';
  password = ''; clearInterval(polling); el('password').value = '';
  el('settings').hidden = true; el('logout').hidden = true; el('login').hidden = false;
  el('error').textContent = message;
}
async function api(path, method = 'GET', body) {
  const r = await fetch(path, {method,
    headers: {Authorization: 'Bearer ' + password, ...(body ? {'Content-Type': 'application/json'} : {})},
    body: body ? JSON.stringify(body) : undefined, cache: 'no-store'});
  const d = await r.json();
  if (r.status === 401) { signedOut('Incorrect or missing password. Please sign in again.'); throw Error('Please sign in again.'); }
  if (!r.ok) throw Error(d.error || 'Request failed');
  return d;
}
const tabs = [...document.querySelectorAll('[role=tab]')];
function selectTab() {
  const selected = tabs.find(t => t.dataset.tab === location.hash.slice(1)) || tabs[0];
  for (const t of tabs) {
    const active = t === selected;
    t.setAttribute('aria-selected', String(active)); t.tabIndex = active ? 0 : -1;
    el(t.getAttribute('aria-controls')).hidden = !active;
  }
}
for (const t of tabs) {
  t.onclick = () => { location.hash = t.dataset.tab; selectTab(); };
  t.onkeydown = e => {
    let i = tabs.indexOf(t);
    if (e.key === 'ArrowRight') i = (i + 1) % tabs.length;
    else if (e.key === 'ArrowLeft') i = (i + tabs.length - 1) % tabs.length;
    else if (e.key === 'Home') i = 0;
    else if (e.key === 'End') i = tabs.length - 1;
    else return;
    e.preventDefault(); tabs[i].click(); tabs[i].focus();
  };
}
window.addEventListener('hashchange', selectTab); selectTab();
function cell(tr, text) {
  const td = document.createElement('td'); td.textContent = text; tr.append(td); return td;
}
function iconButton(parent, icon, label, fn, danger = false) {
  const b = document.createElement('button'); b.type = 'button';
  b.className = 'icon-button ' + (danger ? 'danger' : 'secondary');
  b.title = label; b.setAttribute('aria-label', label);
  const glyph = document.createElement('span');
  glyph.textContent = icon; glyph.setAttribute('aria-hidden', 'true'); b.append(glyph);
  b.disabled = siteBusy; b.onclick = () => fn(b); parent.append(b); return b;
}
function drawSites() {
  el('sites').replaceChildren(); remainingCells.clear();
  for (const s of rules.sites) {
    const tr = document.createElement('tr'), name = cell(tr, '');
    const domain = document.createElement('strong'); domain.textContent = s.domain;
    const domains = document.createElement('small'); domains.className = 'site-domains';
    domains.textContent = s.domains.filter(d => d !== s.domain).join(', ') || 'Main domain only';
    name.append(domain, domains);
    const group = rules.groups.find(g => g.id === s.group_id);
    if (group) { const tag = document.createElement('small'); tag.className = 'site-domains'; tag.textContent = group.name + ' · ' + budgetModeLabel(s.budget_mode); name.append(tag); }
    cell(tr, s.mode === 'block' ? '—' : duration(s.budget_mode === 'shared' && group ? group.daily_limit_seconds : s.daily_limit_seconds));
    cell(tr, s.mode === 'block' ? 'Always block' : 'Daily limit');
    const status = document.createElement('span');
    status.className = 'status-pill' + (s.enabled ? ' enabled' : ''); status.textContent = s.enabled ? 'Enabled' : 'Paused';
    cell(tr, '').append(status);
    const remaining = cell(tr, 'Loading…'); remaining.className = 'site-remaining'; remainingCells.set(s.domain, remaining);
    const actions = document.createElement('div'); actions.className = 'row-actions'; cell(tr, '').append(actions);
    const editButton = iconButton(actions, '✎', 'Edit ' + s.domain, b => openEditor(s, b));
    editButton.setAttribute('aria-controls', 'site-editor');
    editButton.setAttribute('aria-expanded', String(!el('site-editor').hidden && editing === s.domain));
    iconButton(actions, s.enabled ? '⏸' : '▶', (s.enabled ? 'Pause ' : 'Enable ') + s.domain, () => siteAction(s, 'PATCH'));
    iconButton(actions, '🗑', 'Remove ' + s.domain, () => siteAction(s, 'DELETE'), true);
    const reset = iconButton(actions, '↺', 'Reset statistics for ' + s.domain, () => resetSiteStatistics(s));
    reset.dataset.siteReset = s.domain;
    el('sites').append(tr);
  }
  if (!rules.sites.length) {
    const tr = document.createElement('tr'), empty = cell(tr, 'No sites yet. Use “+ Add site” to create your first rule.');
    empty.colSpan = 6; empty.className = 'empty-state'; el('sites').append(tr);
  }
  updateSiteRemaining();
}
// Mutation responses contain the full document. Apply it without a second fetch
// that could fail after a successful save, preserving a Media exceptions draft.
function applyRules(next, updateBypass = false) {
  const pristine = el('bypass').value === rules.bypass_domains.join('\n');
  rules = {...next, groups: next.groups || []}; drawSites(); drawGroups(); groupChoices();
  if (!el('site-editor').hidden) optionalFields();
  if (updateBypass || pristine) el('bypass').value = rules.bypass_domains.join('\n');
}
async function refreshRules(updateBypass = false) { applyRules(await api('/api/sites'), updateBypass); }
function duration(value) {
  if (value <= 0) return '0 min';
  if (value < 60) return '<1 min';
  const minutes = Math.ceil(value / 60);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60), rest = minutes % 60;
  return `${hours} h` + (rest ? ` ${rest} min` : '');
}
function updateSiteRemaining() {
  const user = el('sites-user').value, bySite = new Map();
  for (const row of siteOverview) if (row.user === user) bySite.set(row.site,row);
  for (const [domain, td] of remainingCells) {
    const row = bySite.get(domain);
    td.textContent = stateUnavailable ? 'Unavailable' : !stateLoaded ? 'Loading…' : !row ? '—'
      : row.state === 'always_blocked' ? 'Always blocked'
      : duration(row.remaining_seconds) + (row.state === 'paused' ? ' · Paused' : row.state === 'scheduled_break' ? ' · Scheduled break' : '');
  }
  for (const b of el('sites').querySelectorAll('button')) if (b.dataset.siteReset) b.disabled = siteBusy || !user || stateUnavailable;
}
function updateSiteStatistics(d) {
  siteOverview = d.overview; stateLoaded = true; stateUnavailable = false;
  const users = [...new Set(d.overview.map(r => r.user))].sort(), select = el('sites-user');
  const current = select.value;
  if (JSON.stringify(users) !== JSON.stringify([...select.children].map(o => o.value))) {
    select.replaceChildren();
    for (const user of users) {const option = document.createElement('option'); option.value = user; option.textContent = user; select.append(option);}
    select.value = users.includes(current) ? current : users[0] || '';
  }
  select.disabled = siteBusy || !users.length;
  el('sites-live-error').textContent = ''; updateSiteRemaining();
}
el('sites-user').onchange = updateSiteRemaining;
async function resetSiteStatistics(s) {
  const user = el('sites-user').value; if (siteBusy || !user) return;
  if (!confirm(`Reset today's statistics for ${s.domain} / ${user}? This clears used time, clicks, visits and extra-time uses. Group usage stays recorded.`)) return;
  setSiteBusy(true); el('sites-error').textContent = ''; el('notice').textContent = '';
  try {
    await api('/api/sites/' + encodeURIComponent(s.domain) + '/reset', 'POST', {user});
    el('notice').textContent = `Reset today's statistics for ${s.domain} / ${user}.`;
    try { await refresh(); } catch (error) { el('sites-error').textContent = 'Statistics were reset, but refreshing failed: ' + error.message; }
  } catch (error) { el('sites-error').textContent = error.message; }
  finally {setSiteBusy(false); const button = [...el('sites').querySelectorAll('button')].find(b => b.dataset.siteReset === s.domain); if (button) button.focus();}
}
// A single in-flight refresh serves polling and explicit mutation refreshes.
let refreshPending;
function refresh() {
  if (refreshPending) return refreshPending;
  refreshPending = (async () => {
    const d = await api('/api/state'); if (!password) return;
    drawOverview(d); updateSiteStatistics(d);
    const g = d.diagnostics;
    setText(el('diagnostics'), `Instance: ${d.instance_id} · sessions: ${d.sessions} · HTML injections: ${g.html_injections} · failures: ${g.injection_failures} · sessions created: ${g.sessions_created} · heartbeats: ${g.heartbeats_received}`);
    el('overview-error').textContent = '';
  })().finally(() => { refreshPending = null; });
  return refreshPending;
}
const overviewEntries = new Map();
function setHidden(node, hidden) { if (node.hidden !== hidden) node.hidden = hidden; }
function setClass(node, name) { if (node.className !== name) node.className = name; }
function setText(node, text) { if (node.textContent !== text) node.textContent = text; }
function setAttr(node, name, value) {value = String(value); if (node.getAttribute(name) !== value) node.setAttribute(name, value);}
function setStyle(node, name, value) {if (node.style[name] !== value) node.style[name] = value;}
function overviewNode(tag, className, parent) {
  const node = document.createElement(tag); node.className = className;
  if (parent) parent.append(node); return node;
}
// Do not move or detach nodes on unchanged snapshots: this retains focus/open state.
function reconcileNodes(parent, wanted) {
  const keep = new Set(wanted);
  for (const node of [...parent.children]) if (!keep.has(node)) node.remove();
  for (let i = 0; i < wanted.length; i++) if (parent.children[i] !== wanted[i]) parent.insertBefore(wanted[i], parent.children[i] || null);
}
const budgetStateLabels = {paused: 'Paused', always_blocked: 'Always blocked', scheduled_break: 'Scheduled break', limit_reached: 'Daily limit reached', group_limit_reached: 'Group limit reached'};
function timelineConfig(row, config) {
  // The fallback keeps a readable budget when talking to an older server.
  return config || {base_budget_seconds: row.site_budget_seconds ?? row.budget_seconds};
}
function overviewTimelineModel(row, rule) {
  const shared = row.budget_mode === 'shared';
  const gray = rule.grayscale_enabled ? rule.grayscale_after_seconds : null;
  const blur = rule.blur_enabled ? rule.blur_after_seconds : null;
  const filters = [gray != null && {kind:'grayscale', name:'Grayscale', at:gray}, blur != null && {kind:'blur', name:'Blur', at:blur}].filter(Boolean).sort((a,b) => a.at-b.at);
  const base = shared ? Math.max(0,...filters.map(f => f.at)) : Math.max(0,rule.base_budget_seconds || 0);
  const granted = !shared && rule.extra_time_enabled ? Math.max(0,row.extra_time_granted_seconds || 0) : 0;
  const optional = !shared && rule.extra_time_enabled ? Math.max(0,row.extra_time_uses_remaining || 0) * rule.extra_time_seconds : 0;
  const usable = base + granted, total = usable + optional;
  const points = [...new Set([0,base,usable,total,...filters.map(f => f.at).filter(at => at >= 0 && at <= total)])].sort((a,b) => a-b);
  const segments = points.slice(0,-1).map((start,i) => ({start,end:points[i+1], optional:start>=usable && !shared,
    effect: gray != null && start>=gray ? (blur!=null && start>=blur ? 'combined':'grayscale') : blur!=null && start>=blur ? 'blur':'normal'}));
  const summary = filters.map(f => `${f.name} from ${previewTime(f.at)}`);
  if (granted) summary.push(`Extra granted +${previewTime(Math.ceil(granted))}`);
  if (optional) summary.push(`Optional extra up to +${previewTime(optional)}`);
  if (shared && filters.length) summary.push('Own time; access follows the group budget');
  if (row.budget_mode === 'capped') summary.push('Group budget may block earlier');
  return {base,usable,total,segments,filters,summary:summary.join(' · '),shared};
}
function createBudgetView(parent, group = false) {
  parent.className += ' budget-cell';
  const label = overviewNode('span','budget-text',parent);
  const track = overviewNode(group ? 'progress':'div',group ? 'budget-progress':'overview-line',parent);
  const status = overviewNode('span','budget-status',parent); status.hidden = true;
  const note = overviewNode('small','overview-timeline-note',parent); note.hidden = group;
  const view = {label,track,status,note,group};
  if (!group) {
    track.setAttribute('role','img');
    view.bands = overviewNode('div','overview-bands',track); view.bands.setAttribute('aria-hidden','true');
    view.used = overviewNode('span','overview-used',track); view.used.setAttribute('aria-hidden','true');
    view.now = overviewNode('span','overview-now',track); view.now.setAttribute('aria-hidden','true');
  }
  return view;
}
function updateBudgetView(view,row,config,signature) {
  const blocked = row.state === 'always_blocked', shared = !view.group && row.budget_mode === 'shared';
  setHidden(view.label,blocked);
  const label = shared ? `${duration(row.used_seconds)} used today` : `${duration(Math.max(0,Math.min(row.budget_seconds,row.remaining_seconds)))} left of ${duration(row.budget_seconds)}`;
  setText(view.label,label);
  const stateText = budgetStateLabels[row.state] || ''; setHidden(view.status,!stateText);
  setText(view.status,stateText); setClass(view.status,'budget-status ' + row.state);
  if (view.group) {
    setHidden(view.track,blocked);
    const seconds = Math.max(0,Math.min(row.budget_seconds,row.remaining_seconds));
    if (view.track.max !== (row.budget_seconds || 1)) view.track.max = row.budget_seconds || 1;
    if (Number(view.track.value) !== seconds) view.track.value = seconds;
    setClass(view.track,'budget-progress ' + (row.state==='paused' ? 'paused' : seconds<=0 ? 'exhausted' : seconds/row.budget_seconds<=0.2 ? 'low':'available'));
    setAttr(view.track,'aria-label',`Time remaining for ${row.user} on ${row.site}`); setAttr(view.track,'aria-valuetext',(stateText ? stateText+'. ' : '')+label);
    return;
  }
  const rule = timelineConfig(row,config);
  const key = JSON.stringify([signature || rule,row.budget_mode,row.extra_time_granted_seconds,row.extra_time_uses_remaining]);
  if (view.signature !== key) {
    view.signature = key; view.model = overviewTimelineModel(row,rule);
    const m = view.model; view.bands.replaceChildren();
    for (const segment of m.segments) {
      const band = overviewNode('span','overview-band effect-'+segment.effect+(segment.optional ? ' overview-optional':''),view.bands);
      band.style.width = ((segment.end-segment.start)/m.total*100)+'%';
    }
    // Marker positions depend on settings, not live usage.
    for (const marker of view.markers || []) marker.remove(); view.markers = [];
    const mark = (at,kind,title) => {
      const node = overviewNode('span','overview-threshold '+kind,view.track); node.setAttribute('aria-hidden','true');
      node.style.left = (at/m.total*100)+'%'; node.title = title; view.markers.push(node);
    };
    if (m.total>0) {
      for (const f of m.filters) if (f.at<=m.total) mark(f.at,'threshold-'+f.kind,`${f.name}: ${previewTime(f.at)}`);
      if (!shared) mark(m.base,'threshold-limit',`Daily limit: ${previewTime(m.base)}`);
    }
    setText(view.note,m.summary);
  }
  const m = view.model;
  setHidden(view.track,blocked || m.total<=0); setHidden(view.note,blocked || !m.summary);
  const used = Math.max(0,row.used_seconds || 0), percent = m.total>0 ? Math.min(100,used/m.total*100)+'%' : '0%';
  setStyle(view.used,'width',percent); setStyle(view.now,'left',percent);
  setAttr(view.track,'data-state',row.state);
  const accessible = `${stateText ? stateText+'. ' : ''}${label}. Used: ${previewTime(Math.floor(used))}. ${m.summary}`;
  setAttr(view.track,'aria-label',accessible); if (view.track.title !== accessible) view.track.title = accessible;
}
function renderGroupMembers(entry) {
  if (!entry.details.open) return;
  const wanted = [], alive = new Set();
  for (const row of entry.rows) {
    alive.add(row.site); let member = entry.members.get(row.site);
    if (!member) {
      const node = overviewNode('section','group-member');
      member = {node,name:overviewNode('strong','',node),mode:overviewNode('small','',node),budget:createBudgetView(node)};
      entry.members.set(row.site,member);
    }
    setText(member.name,row.site); setText(member.mode,budgetModeLabel(row.budget_mode));
    const setting = entry.settings.get(row.site);
    updateBudgetView(member.budget,row,setting?.rule,setting?.signature); wanted.push(member.node);
  }
  for (const [key,member] of entry.members) if (!alive.has(key)) {member.node.remove();entry.members.delete(key);}
  reconcileNodes(entry.memberBody,wanted);
}
function drawOverview(d) {
  setText(el('overview-date'),d.date); setAttr(el('overview-date'),'datetime',d.date);
  setText(el('meta'),'Daily budgets for all configured users and sites.');
  const settings = new Map(Object.entries(d.timeline_rules || {}).map(([site,rule]) => [site,{rule,signature:JSON.stringify(rule)}]));
  const users = new Map();
  const userBucket = user => {if (!users.has(user)) users.set(user,{groups:[],sites:[],members:new Map()});return users.get(user);};
  for (const group of d.group_overview || []) userBucket(group.user).groups.push(group);
  for (const row of d.overview) {
    const bucket = userBucket(row.user);
    if (row.group_id) {if (!bucket.members.has(row.group_id)) bucket.members.set(row.group_id,[]);bucket.members.get(row.group_id).push(row);}
    else bucket.sites.push(row);
  }
  const wanted=[],alive=new Set();
  for (const user of [...users.keys()].sort()) {
    const bucket = users.get(user);
    for (const group of bucket.groups) {
      const key = JSON.stringify(['group',user,group.group_id]);alive.add(key);let entry=overviewEntries.get(key);
      if (!entry) {
        const node=overviewNode('tr','group-budget-row'),userCell=cell(node,user),name=cell(node,'');
        const budget=createBudgetView(cell(node,''),true),detailRow=overviewNode('tr','group-detail-row'),td=cell(detailRow,'');td.colSpan=3;
        const details=overviewNode('details','',td),summary=overviewNode('summary','',details),memberBody=overviewNode('div','group-members',details);
        details.dataset.key=key;summary.dataset.groupSummary=key;
        entry={node,userCell,name,budget,detailRow,details,summary,memberBody,members:new Map()};overviewEntries.set(key,entry);
        details.addEventListener('toggle',()=>renderGroupMembers(entry));
      }
      entry.rows=bucket.members.get(group.group_id)||[];entry.settings=settings;
      setText(entry.name,group.name);setText(entry.summary,`${entry.rows.length} site${entry.rows.length===1?'':'s'} in ${group.name}`);
      setAttr(entry.summary,'aria-label',`Show sites in ${group.name} for ${user}`);
      updateBudgetView(entry.budget,{...group,site:'group '+group.name});renderGroupMembers(entry);
      wanted.push(entry.node,entry.detailRow);
    }
    for (const row of bucket.sites) {
      const key=JSON.stringify(['site',user,row.site]);alive.add(key);let entry=overviewEntries.get(key);
      if (!entry) {const node=overviewNode('tr','');cell(node,user);cell(node,row.site);entry={node,budget:createBudgetView(cell(node,''))};overviewEntries.set(key,entry);}
      const setting=settings.get(row.site);updateBudgetView(entry.budget,row,setting?.rule,setting?.signature);wanted.push(entry.node);
    }
  }
  if (!wanted.length) alive.add('empty');
  for (const key of overviewEntries.keys()) if (!alive.has(key)) overviewEntries.delete(key);
  if (!wanted.length) {
    const key='empty';let entry=overviewEntries.get(key);
    if (!entry) {const node=overviewNode('tr','');cell(node,'No sites configured. Add a site in Sites & limits to see its daily budget.').colSpan=3;entry={node};overviewEntries.set(key,entry);}
    wanted.push(entry.node);
  }
  reconcileNodes(el('rows'),wanted);
}
function draftSnapshot() {
  return JSON.stringify(draftFields.map(id => el(id).type === 'checkbox' ? el(id).checked : el(id).value));
}
function isDirty() { return (!el('site-editor').hidden && draftSnapshot() !== editorBaseline) || (!el('group-editor').hidden && groupSnapshot() !== groupBaseline); }
function discardAllowed() { return !isDirty() || confirm('Discard unsaved changes?'); }
function clearFieldError(id) {
  el(id).removeAttribute('aria-invalid'); el(id + '-error').hidden = true; el(id + '-error').textContent = '';
}
function clearErrors() { for (const id of validatedFields) clearFieldError(id); el('form-error').textContent = ''; }
function fieldError(id, message) {
  if (id === 'domains' || id === 'preset-bypass') el('advanced-domains').open = true;
  el(id).setAttribute('aria-invalid', 'true');
  el(id + '-error').textContent = message; el(id + '-error').hidden = false; el(id).focus(); return null;
}
function updateDomainSummary() {
  const domains = list(el('domains').value), bypass = list(el('preset-bypass').value);
  const preview = domains.slice(0, 3).join(', ') + (domains.length > 3 ? `, +${domains.length - 3} more` : '');
  el('domains-summary').textContent = domains.length ? `${domains.length} target domain${domains.length === 1 ? '' : 's'}: ${preview}`
    : 'Target domains will appear here after entering a main domain.';
  if (!editing && bypass.length) el('domains-summary').textContent += ` · ${bypass.length} media exception${bypass.length === 1 ? '' : 's'} to add`;
}
// All timeline positions use configured active seconds, never today's counters.
function previewTime(seconds) {
  const minutes = Math.floor(seconds / 60), rest = seconds % 60;
  return minutes ? `${minutes} min` + (rest ? ` ${rest} sec` : '') : `${rest} sec`;
}
function limitPreviewModel({shared, budget, gray, blur, extraEnabled = false, extraSeconds = 0, extraUses = 0}) {
  const filters = [gray != null && {kind: 'grayscale', name: 'Grayscale', at: gray},
    blur != null && {kind: 'blur', name: 'Blur', at: blur}].filter(Boolean).sort((a, b) => a.at - b.at);
  if ((!shared && !validSeconds(budget)) || filters.some(f => !validSeconds(f.at) || (!shared && f.at >= budget)) ||
      (extraEnabled && !shared && (!validSeconds(extraSeconds) || !Number.isInteger(extraUses) || extraUses < 1 || extraUses > 100)))
    return {error: 'Enter valid budget, filter thresholds and extra time to see the timeline.'};
  if (shared && !filters.length) return {empty: true};
  const base = shared ? Math.max(...filters.map(f => f.at)) : budget;
  const bonus = shared || !extraEnabled ? 0 : extraSeconds * extraUses, total = base + bonus;
  const points = [...new Set([0, ...filters.map(f => f.at), base, total])].sort((a, b) => a - b);
  const effectAt = at => gray != null && at >= gray ? (blur != null && at >= blur ? 'combined' : 'grayscale')
    : blur != null && at >= blur ? 'blur' : 'normal';
  const segments = points.slice(0, -1).map((start, i) => ({start, end: points[i + 1],
    effect: effectAt(start), bonus: !shared && start >= base}));
  return {base, bonus, total, filters, segments};
}
function renderLimitPreview() {
  const grouped = !!el('site-group').value, mode = grouped ? el('budget-mode').value : 'independent';
  const shared = mode === 'shared', group = rules.groups.find(g => g.id === el('site-group').value);
  el('shared-budget').hidden = !shared;
  el('shared-budget').textContent = shared && group ? `${group.name}: ${previewTime(group.daily_limit_seconds)} shared group budget` : '';
  const seconds = id => Math.round(Number(el(id).value) * 60);
  const extras = !shared && el('extra-enabled').checked;
  const model = limitPreviewModel({shared, budget: seconds('minutes'),
    gray: el('grayscale-enabled').checked ? seconds('grayscale-minutes') : null,
    blur: el('blur-enabled').checked ? seconds('blur-minutes') : null,
    extraEnabled: extras, extraSeconds: extras ? seconds('extra-minutes') : 0,
    extraUses: extras ? Number(el('extra-uses').value) : 0});
  el('preview-title').textContent = shared ? "This site's filter timeline" : 'Your daily timeline';
  el('preview-note').textContent = shared
    ? "Filters use this site's own active time. The group budget is spent by all its sites; the end of this preview is not a blocking time."
    : "Preview of these settings, not today's usage." + (mode === 'capped' ? ' The group budget may block access earlier; extra time cannot extend it.' : '');
  el('preview-track').replaceChildren(); el('preview-legend').replaceChildren();
  el('preview-summary').textContent = ''; el('preview-end').textContent = '';
  el('preview-chart').hidden = !!model.error || !!model.empty;
  el('preview-message').hidden = !model.error && !model.empty;
  el('preview-message').textContent = model.error || (model.empty ? 'Enable grayscale or blur to preview its start time. Access is controlled by the shared group budget.' : '');
  if (model.error || model.empty) return;
  const track = el('preview-track'), legend = el('preview-legend');
  const item = (kind, text) => {
    const li = document.createElement('li'), swatch = document.createElement('span'), label = document.createElement('span');
    swatch.className = 'preview-swatch effect-' + kind; swatch.setAttribute('aria-hidden', 'true'); label.textContent = text;
    li.append(swatch, label); legend.append(li);
  };
  const effectNames = {normal: 'Normal', grayscale: 'Grayscale', blur: 'Blur', combined: 'Grayscale + blur'};
  for (const segment of model.segments) {
    const node = document.createElement('span');
    node.className = 'preview-segment effect-' + segment.effect + (segment.bonus ? ' preview-bonus' : '');
    node.style.width = ((segment.end - segment.start) / model.total * 100) + '%';
    track.append(node);
    item(segment.effect, `${previewTime(segment.start)}–${previewTime(segment.end)}: ${effectNames[segment.effect]}` + (segment.bonus ? ' (optional extra time)' : ''));
  }
  const marker = (at, kind, text) => {
    const node = document.createElement('span'); node.className = 'preview-marker marker-' + kind;
    node.style.left = (at / model.total * 100) + '%'; node.title = text; track.append(node);
  };
  for (const f of model.filters) marker(f.at, f.kind, `${f.name} starts at ${previewTime(f.at)}`);
  // Explicit start labels also describe a filter starting at the shared preview's end.
  for (const f of model.filters) item(f.kind, `${f.name} starts at ${previewTime(f.at)}`);
  if (!shared) {
    marker(model.base, 'limit', `Daily limit: ${previewTime(model.base)}`);
    item('limit', `Daily limit: ${previewTime(model.base)}`);
  }
  el('preview-summary').textContent = shared ? 'Own active time · filter start times'
    : model.bonus ? `${previewTime(model.base)} + ${el('extra-uses').value} × ${previewTime(seconds('extra-minutes'))} = maximum ${previewTime(model.total)}`
    : `Daily limit: ${previewTime(model.base)}`;
  if (model.bonus) {
    const method = {click: 'Click to extend', math: 'Solve a math problem for each extension', reflection: 'Write a reflection and take a 10-second pause for each extension'}[el('extra-method').value || 'click'];
    item('bonus', `Extra time is optional and must be requested manually; active filters continue. ${method}.`);
  }
  el('preview-end').textContent = shared ? `${previewTime(model.total)} · last filter starts` : previewTime(model.total);
}
function optionalFields() {
  const limit = !el('always-block').checked;
  const shared = !!el('site-group').value && el('budget-mode').value === 'shared';
  el('budget-mode-field').hidden = !el('site-group').value;
  el('budget-help').textContent = !el('site-group').value ? 'This site has its own daily budget.' : {
    shared: 'This site spends the shared group budget. It has no separate time limit.',
    capped: 'This site spends group time and has its own cap. Whichever runs out first blocks access. Extra time cannot bypass the group limit.',
    independent: 'This site is listed in the group but spends only its own budget.'
  }[el('budget-mode').value];
  el('minutes-field').hidden = !limit || shared; el('optional-limits').hidden = !limit; el('minutes').disabled = !limit || shared;
  el('always-block').setAttribute('aria-expanded', String(limit));
  el('shared-extra-help').hidden = !shared;
  el('blur-help').textContent = shared ? "Measured against this site's own active time, not the group's total." : 'Must be earlier than the daily budget.';
  el('grayscale-help').textContent = shared ? "Measured against this site's own active time, not the group's total." : 'Must be earlier than the daily budget.';
  el('access-help').textContent = limit ? "Active time is shared across the site's domains and browser tabs."
    : 'This site is blocked whenever the rule is enabled.';
  for (const [toggle, fields] of [['blur-enabled', 'blur-fields'], ['grayscale-enabled', 'grayscale-fields'], ['extra-enabled', 'extra-fields']]) {
    const active = limit && el(toggle).checked && !(shared && toggle === 'extra-enabled');
    el(toggle).disabled = !limit || (shared && toggle === 'extra-enabled'); el(toggle).setAttribute('aria-expanded', String(active)); el(fields).hidden = !active;
  }
  el('blur-minutes').disabled = !limit || !el('blur-enabled').checked;
  el('grayscale-minutes').disabled = !limit || !el('grayscale-enabled').checked;
  el('extra-method').disabled = el('extra-minutes').disabled = el('extra-uses').disabled = !limit || shared || !el('extra-enabled').checked;
  renderLimitPreview();
}
function openEditor(s = null, trigger = el('add-site')) {
  if (siteBusy) return;
  if (!el('site-editor').hidden && editing === (s?.domain || '')) { focusEditor(); return; }
  if (!discardAllowed()) return;
  closeGroupEditor(false);
  editing = s?.domain || ''; editorReturnFocus = trigger;
  el('site-form').reset(); clearErrors(); autoDomains = '';
  el('edit-title').textContent = s ? 'Edit ' + s.domain : 'Add a site';
  el('editor-intro').textContent = s ? 'Update the rule for this site. Changes apply after saving.'
    : 'Choose a preset or enter a domain to get started.';
  el('save').textContent = s ? 'Save changes' : 'Add site';
  el('domain').value = s?.domain || ''; el('domain').disabled = !!s;
  el('domain-field').hidden = el('preset-field').hidden = !!s; el('preset').disabled = !!s;
  el('domain-display').hidden = !s; el('domain-display').textContent = s ? 'Main domain: ' + s.domain : '';
  el('domains').value = s ? s.domains.join('\n') : '';
  el('minutes').value = s ? s.daily_limit_seconds / 60 : 15;
  el('always-block').checked = s?.mode === 'block'; el('enabled').checked = s ? s.enabled : true;
  groupChoices(); el('site-group').value = s?.group_id || ''; el('budget-mode').value = s?.budget_mode || 'shared';
  el('blur-enabled').checked = !!s?.blur_enabled;
  el('blur-minutes').value = s?.blur_after_seconds ? s.blur_after_seconds / 60 : '';
  el('grayscale-enabled').checked = !!s?.grayscale_enabled;
  el('grayscale-minutes').value = s?.grayscale_after_seconds ? s.grayscale_after_seconds / 60 : '';
  el('extra-enabled').checked = !!s?.extra_time_enabled;
  el('extra-method').value = s?.extra_time_method || 'click';
  el('extra-minutes').value = (s?.extra_time_seconds || 180) / 60; el('extra-uses').value = s?.extra_time_max_uses || 1;
  el('preset-bypass').value = ''; el('preset-bypass').disabled = !!s; el('preset-bypass-field').hidden = !!s;
  el('preset-note').textContent = ''; el('advanced-domains').open = false;
  optionalFields(); updateDomainSummary(); el('site-editor').hidden = false;
  el('add-site').setAttribute('aria-expanded', String(!s)); el('sites-error').textContent = ''; el('notice').textContent = '';
  editorBaseline = draftSnapshot(); drawSites(); focusEditor();
}
function focusEditor() {
  el('site-editor').scrollIntoView({block: 'start'}); el(editing ? 'always-block' : 'preset').focus({preventScroll: true});
}
function closeEditor(restoreFocus = true) {
  el('site-editor').hidden = true; editing = ''; editorBaseline = ''; el('add-site').setAttribute('aria-expanded', 'false'); drawSites();
  if (restoreFocus) {
    const label = editorReturnFocus?.getAttribute('aria-label');
    const replacement = [...el('sites').querySelectorAll('button')].find(b => label && b.getAttribute('aria-label') === label);
    (replacement || el('add-site')).focus();
  }
}
function setSiteBusy(busy) {
  siteBusy = busy; el('sites-user').disabled = busy || !el('sites-user').children.length; el('editor-fields').disabled = busy; el('site-form').setAttribute('aria-busy', String(busy));
  el('group-fields').disabled = busy; el('add-group').disabled = busy;
  el('add-site').disabled = el('logout').disabled = busy;
  for (const id of ['download-config', 'import-config', 'config-file', 'save-bypass', 'bypass', 'reset']) el(id).disabled = busy;
  for (const b of el('sites').querySelectorAll('button')) b.disabled = busy;
  for (const b of el('groups').querySelectorAll('button')) b.disabled = busy;
  updateSiteRemaining();
}
el('download-config').onclick = async () => {
  if (siteBusy) return;
  setSiteBusy(true); el('config-error').textContent = '';
  try {
    const saved = await api('/api/sites');
    const url = URL.createObjectURL(new Blob([JSON.stringify(saved, null, 2) + '\n'], {type: 'application/json'}));
    const link = document.createElement('a'); link.href = url; link.download = 'pauseproxy-sites.json';
    document.body.append(link);
    try { link.click(); } finally { link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000); }
  } catch (error) { el('config-error').textContent = 'Could not download config: ' + error.message; }
  finally { setSiteBusy(false); if (password) el('download-config').focus(); }
};
el('import-config').onclick = () => { if (!siteBusy) el('config-file').click(); };
el('config-file').onchange = async () => {
  const input = el('config-file'), file = input.files?.[0]; input.value = '';
  if (siteBusy || !file) return;
  setSiteBusy(true); el('config-error').textContent = ''; el('notice').textContent = '';
  try {
    if (file.size > 1024 * 1024) throw Error('Config must be at most 1 MiB.');
    let contents;
    try { contents = await file.text(); } catch { throw Error('Could not read the selected file.'); }
    let imported;
    try { imported = JSON.parse(contents); } catch { throw Error('The selected file is not valid JSON.'); }
    if (!imported || typeof imported !== 'object' || Array.isArray(imported)) throw Error('Config must be a JSON object.');
    if (!confirm('Replace all saved sites, groups and media exceptions in this instance with this config? This affects all users. Statistics will be kept.')) return;
    if (!discardAllowed()) return;
    if (el('bypass').value !== rules.bypass_domains.join('\n') && !confirm('Discard unsaved media exceptions?')) return;
    const saved = await api('/api/sites', 'PUT', imported);
    closeEditor(false); closeGroupEditor(false); applyRules(saved, true);
    el('media-error').textContent = el('media-notice').textContent = '';
    el('notice').textContent = 'Imported config. Refresh PAC using the Connection tab; restart the browser after changing media exceptions.';
    try { await refresh(); } catch (error) { el('config-error').textContent = 'Config was imported, but refreshing statistics failed: ' + error.message; }
  } catch (error) { el('config-error').textContent = error.message; }
  finally { setSiteBusy(false); if (password) el('import-config').focus(); }
};
async function siteAction(s, method) {
  if (siteBusy) return;
  const affectsEditor = !el('site-editor').hidden && editing === s.domain;
  if (method === 'DELETE' && !confirm('Remove ' + s.domain + '? This deletes its saved rule.')) return;
  if (affectsEditor && !discardAllowed()) return;
  setSiteBusy(true); el('sites-error').textContent = ''; el('notice').textContent = '';
  try {
    applyRules(await api('/api/sites/' + encodeURIComponent(s.domain), method, method === 'PATCH' ? {enabled: !s.enabled} : undefined));
    if (affectsEditor) closeEditor(false);
    el('notice').textContent = s.domain + (method === 'DELETE' ? ' removed. Refresh PAC using the Connection tab.'
      : s.enabled ? ' paused. Refresh PAC using the Connection tab.' : ' enabled. Refresh PAC using the Connection tab.');
  } catch (e) { el('sites-error').textContent = e.message; }
  finally { setSiteBusy(false); if (password) el('add-site').focus(); }
}
function normalizeDomain(value) {
  let d = value.trim().toLowerCase().replace(/\.$/, '');
  if (!d || /[\s/:*\\@?#]/.test(d)) return null;
  try { d = new URL('https://' + d).hostname; } catch { return null; }
  if (d.length > 253 || !d.includes('.') || /^[\d.]+$/.test(d)) return null;
  return d.split('.').every(part => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(part)) ? d : null;
}
function syncMainDomain() {
  if (!editing && (!el('domains').value.trim() || el('domains').value === autoDomains)) {
    const d = normalizeDomain(el('domain').value);
    autoDomains = d ? d + (d.startsWith('www.') ? '' : '\nwww.' + d) : ''; el('domains').value = autoDomains;
  }
  updateDomainSummary();
}
const validSeconds = n => Number.isInteger(n) && n >= 1 && n <= 86400;
function siteBody() {
  clearErrors(); syncMainDomain();
  const domain = normalizeDomain(el('domain').value);
  if (!domain) return fieldError('domain', 'Enter a valid domain without a URL, port or wildcard.');
  if (!editing && rules.sites.some(s => s.domain === domain)) return fieldError('domain', 'This site already has a rule. Edit it from the list.');
  const targets = list(el('domains').value).map(normalizeDomain);
  if (!targets.length || targets.length > 32 || targets.some(d => !d)) return fieldError('domains', 'Enter 1–32 valid target domains, without URLs, ports or wildcards.');
  if (new Set(targets).size !== targets.length) return fieldError('domains', 'Each target domain must appear only once.');
  if (!targets.includes(domain)) return fieldError('domains', 'Include the main domain: ' + domain);
  const bypass = editing ? [] : list(el('preset-bypass').value).map(normalizeDomain);
  if (bypass.some(d => !d) || bypass.length > 128) return fieldError('preset-bypass', 'Enter valid media domains, without URLs, ports or wildcards (up to 128).');
  const conflict = targets.find(d => rules.sites.some(s => s.domain !== editing && s.domains.includes(d)));
  if (conflict) return fieldError('domains', conflict + ' already belongs to another rule.');
  const exceptions = [...rules.bypass_domains, ...bypass];
  const bypassConflict = targets.find(d => exceptions.some(b => d === b || d.endsWith('.' + b)));
  if (bypassConflict) return fieldError('domains', bypassConflict + ' is covered by a media exception. Remove the conflicting exception first.');
  const limit = !el('always-block').checked;
  const shared = !!el('site-group').value && el('budget-mode').value === 'shared';
  let seconds = Math.round(Number(el('minutes').value) * 60);
  if (limit && !shared && !validSeconds(seconds)) return fieldError('minutes', 'Use a daily budget between 1 second and 1440 minutes.');
  if (!validSeconds(seconds)) seconds = 900;
  const blur = limit && el('blur-enabled').checked, blurSeconds = Math.round(Number(el('blur-minutes').value) * 60);
  if (blur && (!Number.isInteger(blurSeconds) || blurSeconds < 1 || blurSeconds > 86400 || (!shared && blurSeconds >= seconds)))
    return fieldError('blur-minutes', shared ? 'Use a blur threshold between 1 second and 1440 minutes.' : 'Start blur at least 1 second after zero and before the daily budget.');
  const gray = limit && el('grayscale-enabled').checked, graySeconds = Math.round(Number(el('grayscale-minutes').value) * 60);
  if (gray && (!Number.isInteger(graySeconds) || graySeconds < 1 || graySeconds > 86400 || (!shared && graySeconds >= seconds)))
    return fieldError('grayscale-minutes', shared ? 'Use a grayscale threshold between 1 second and 1440 minutes.' : 'Start grayscale at least 1 second after zero and before the daily budget.');
  const extra = limit && !shared && el('extra-enabled').checked;
  const extraSeconds = Math.round(Number(el('extra-minutes').value) * 60), extraUses = Number(el('extra-uses').value);
  if (extra && !validSeconds(extraSeconds)) return fieldError('extra-minutes', 'Use between 1 second and 1440 minutes per extension.');
  if (extra && (!Number.isInteger(extraUses) || extraUses < 1 || extraUses > 100)) return fieldError('extra-uses', 'Use a whole number from 1 to 100.');
  const body = {domains: targets, enabled: el('enabled').checked, mode: limit ? 'limit' : 'block', daily_limit_seconds: seconds,
    group_id: el('site-group').value, budget_mode: el('site-group').value ? el('budget-mode').value : 'independent',
    blur_enabled: blur, blur_after_seconds: Number.isInteger(blurSeconds) && blurSeconds >= 0 ? blurSeconds : 0,
    grayscale_enabled: gray, grayscale_after_seconds: Number.isInteger(graySeconds) && graySeconds >= 0 ? graySeconds : 0,
    extra_time_method: el('extra-method').value || 'click', extra_time_enabled: extra, extra_time_seconds: validSeconds(extraSeconds) ? extraSeconds : 180,
    extra_time_max_uses: Number.isInteger(extraUses) && extraUses >= 1 && extraUses <= 100 ? extraUses : 1};
  if (!editing) { body.domain = domain; body.bypass_domains = bypass; }
  return body;
}
el('add-site').onclick = () => openEditor();
el('cancel').onclick = () => { if (!siteBusy && discardAllowed()) closeEditor(); };
el('site-form').onsubmit = async e => {
  e.preventDefault(); if (siteBusy) return;
  const body = siteBody(); if (!body) return;
  const domain = editing || body.domain;
  setSiteBusy(true); el('sites-error').textContent = ''; el('notice').textContent = '';
  try {
    applyRules(await api(editing ? '/api/sites/' + encodeURIComponent(editing) : '/api/sites', editing ? 'PATCH' : 'POST', body));
    closeEditor(false); el('notice').textContent = 'Saved ' + domain + '. If domains changed, refresh PAC using the Connection tab.';
  } catch (e) {
    el('form-error').textContent = e.message; el('form-error').tabIndex = -1; el('form-error').focus();
  } finally { setSiteBusy(false); if (el('site-editor').hidden && password) el('add-site').focus(); }
};
el('preset').onchange = () => {
  const p = presets[el('preset').value];
  if (p) {
    el('domain').value = p.domain; el('domains').value = p.domains.join('\n'); autoDomains = '';
    el('preset-bypass').value = p.bypass.join('\n');
  } else { el('preset-bypass').value = ''; }
  el('preset-note').textContent = p ? 'Saving adds the suggested media exceptions. You can review them in Advanced domains.' : '';
  clearErrors(); updateDomainSummary();
};
el('domain').onchange = syncMainDomain;
el('domains').oninput = el('preset-bypass').oninput = updateDomainSummary;
for (const id of validatedFields) el(id).addEventListener('input', () => clearFieldError(id));
for (const id of ['minutes', 'grayscale-minutes', 'blur-minutes', 'extra-minutes', 'extra-uses'])
  el(id).addEventListener('input', renderLimitPreview);
el('blur-enabled').onchange = () => {
  if (el('blur-enabled').checked && !el('blur-minutes').value)
    el('blur-minutes').value = Math.max(1, Math.floor(Number(el('minutes').value) * 60 * 2 / 3) || 1) / 60;
  clearErrors(); optionalFields();
};
el('grayscale-enabled').onchange = () => {
  if (el('grayscale-enabled').checked && !el('grayscale-minutes').value)
    el('grayscale-minutes').value = Math.max(1, Math.floor(Number(el('minutes').value) * 60 * 2 / 3) || 1) / 60;
  clearErrors(); optionalFields();
};
el('extra-method').onchange = renderLimitPreview;
el('extra-enabled').onchange = el('always-block').onchange = () => { clearErrors(); optionalFields(); };
el('site-group').onchange = () => { if (el('site-group').value && !el('budget-mode').value) el('budget-mode').value = 'shared'; clearErrors(); optionalFields(); };
el('budget-mode').onchange = () => { clearErrors(); optionalFields(); };
window.addEventListener('beforeunload', e => { if (password && isDirty()) { e.preventDefault(); e.returnValue = ''; } });
el('login-form').onsubmit = async e => {
  e.preventDefault(); clearInterval(polling); password = el('password').value; el('connect').disabled = true;
  try {
    await refreshRules(true); await refresh();
    el('password').value = ''; el('login').hidden = true; el('settings').hidden = false; el('logout').hidden = false; el('error').textContent = '';
    polling = setInterval(() => {if (refreshPending) return; refresh().catch(e => { el('overview-error').textContent = el('sites-live-error').textContent = e.message; stateUnavailable = true; updateSiteRemaining(); });}, 2000);
  } catch (e) { signedOut(e.message); } finally { el('connect').disabled = false; }
};
el('logout').onclick = () => {
  if (!discardAllowed()) return;
  signedOut(); closeEditor(false); closeGroupEditor(false); el('bypass').value = ''; el('password').focus();
};
el('save-bypass').onclick = async () => {
  if (siteBusy) return;
  setSiteBusy(true); el('save-bypass').disabled = true;
  try {
    applyRules(await api('/api/bypass-domains', 'PUT', {bypass_domains: list(el('bypass').value)}), true);
    el('media-error').textContent = ''; el('media-notice').textContent = 'Saved. Refresh PAC and restart the browser after changing media exceptions.';
  } catch (e) { el('media-error').textContent = e.message; el('media-notice').textContent = ''; }
  finally { setSiteBusy(false); el('save-bypass').disabled = false; }
};
el('reset').onclick = async () => {
  if (siteBusy) return;
  if (!confirm('Reset all in-memory statistics? Saved rules will stay unchanged.')) return;
  setSiteBusy(true);
  try { await api('/api/reset', 'POST', {}); await refresh(); } catch (e) { el('overview-error').textContent = e.message; }
  finally { setSiteBusy(false); }
};
fetch('/connection', {cache: 'no-store'}).then(r => { if (!r.ok) throw Error('Could not load connection details'); return r.json(); }).then(c => {
  el('pac').textContent = c.pac; el('pac-link').href = c.pac; el('ca').href = c.ca; el('demo').href = c.demo;
  el('ca-sha256').textContent = c.ca_sha256 || 'Unavailable';
  el('ca-sha1').textContent = c.ca_sha1 || 'Unavailable';
  if (c.ca_error) el('connection-error').textContent = c.ca_error;
}).catch(e => {
  el('ca-sha256').textContent = 'Unavailable'; el('ca-sha1').textContent = 'Unavailable';
  el('connection-error').textContent = e.message;
});
function budgetModeLabel(mode) {
  return {shared: 'Shared budget', capped: 'Group + site cap', independent: 'Independent budget'}[mode] || 'Independent budget';
}
function groupChoices() {
  const selected = el('site-group').value;
  el('site-group').replaceChildren();
  for (const g of [{id: '', name: 'No group'}, ...rules.groups]) {
    const o = document.createElement('option'); o.value = g.id; o.textContent = g.name; el('site-group').append(o);
  }
  el('site-group').value = rules.groups.some(g => g.id === selected) ? selected : '';
}
function drawGroups() {
  el('groups').replaceChildren();
  for (const g of rules.groups) {
    const tr = document.createElement('tr');
    cell(tr, g.name); cell(tr, duration(g.daily_limit_seconds)); cell(tr, rules.sites.filter(s => s.group_id === g.id).length);
    const actions = document.createElement('div'); actions.className = 'row-actions'; cell(tr, '').append(actions);
    iconButton(actions, '✎', 'Edit group ' + g.name, () => openGroupEditor(g));
    iconButton(actions, '🗑', 'Remove group ' + g.name, () => removeGroup(g), true);
    el('groups').append(tr);
  }
  if (!rules.groups.length) {
    const tr = document.createElement('tr'); cell(tr, 'No groups yet. Add a group to share a budget across sites.').colSpan = 4; el('groups').append(tr);
  }
}
function groupSnapshot() { return JSON.stringify([el('group-name').value, el('group-minutes').value]); }
function closeGroupEditor(focus = true) {
  el('group-editor').hidden = true; editingGroup = ''; groupBaseline = '';
  el('add-group').setAttribute('aria-expanded', 'false'); if (focus) el('add-group').focus();
}
function openGroupEditor(g = null) {
  if (siteBusy) return;
  if (!el('group-editor').hidden && editingGroup === (g?.id || '')) { el('group-name').focus(); return; }
  if (!discardAllowed()) return;
  closeEditor(false); editingGroup = g?.id || '';
  el('group-name').value = g?.name || ''; el('group-minutes').value = g ? g.daily_limit_seconds / 60 : 15;
  el('group-title').textContent = g ? 'Edit ' + g.name : 'Add a group';
  el('save-group').textContent = g ? 'Save changes' : 'Add group';
  clearFieldError('group-name'); clearFieldError('group-minutes'); el('group-error').textContent = '';
  el('group-editor').hidden = false; el('add-group').setAttribute('aria-expanded', 'true');
  groupBaseline = groupSnapshot(); el('group-editor').scrollIntoView({block: 'start'}); el('group-name').focus();
}
el('add-group').onclick = () => openGroupEditor();
el('cancel-group').onclick = () => { if (!siteBusy && discardAllowed()) closeGroupEditor(); };
el('group-form').onsubmit = async e => {
  e.preventDefault(); if (siteBusy) return;
  clearFieldError('group-name'); clearFieldError('group-minutes'); el('group-error').textContent = '';
  const name = el('group-name').value.trim(), seconds = Math.round(Number(el('group-minutes').value) * 60);
  if (!name || [...name].length > 100 || /[\r\n\t]/.test(name)) return fieldError('group-name', 'Enter a group name (up to 100 characters).');
  if (rules.groups.some(g => g.id !== editingGroup && g.name.toLowerCase() === name.toLowerCase())) return fieldError('group-name', 'A group with this name already exists.');
  if (!validSeconds(seconds)) return fieldError('group-minutes', 'Use a budget between 1 second and 1440 minutes.');
  setSiteBusy(true);
  try {
    applyRules(await api(editingGroup ? '/api/groups/' + encodeURIComponent(editingGroup) : '/api/groups', editingGroup ? 'PATCH' : 'POST', {name, daily_limit_seconds: seconds}));
    closeGroupEditor(false); el('notice').textContent = 'Saved group ' + name + '. Changes apply from now on.';
  } catch (e) { el('group-error').textContent = e.message; }
  finally { setSiteBusy(false); if (el('group-editor').hidden) el('add-group').focus(); }
};
async function removeGroup(g) {
  if (siteBusy) return;
  const members = rules.sites.filter(s => s.group_id === g.id);
  const adjustBlur = members.some(s => s.budget_mode === 'shared' && s.blur_enabled && s.blur_after_seconds >= g.daily_limit_seconds);
  const adjustGray = members.some(s => s.budget_mode === 'shared' && s.grayscale_enabled && s.grayscale_after_seconds >= g.daily_limit_seconds);
  if (!confirm(`Remove group ${g.name}? Its ${members.length} site(s) will keep independent budgets. Shared sites inherit ${duration(g.daily_limit_seconds)}. Today's usage stays recorded.` +
    (adjustGray ? ' Grayscale will be turned off where its threshold does not fit the inherited site limit; the threshold value stays saved.' : '') +
    (adjustBlur ? ' Media blur will be turned off where its threshold does not fit the inherited site limit; the threshold value stays saved.' : ''))) return;
  if (!discardAllowed()) return;
  setSiteBusy(true); el('sites-error').textContent = '';
  try {
    applyRules(await api('/api/groups/' + encodeURIComponent(g.id), 'DELETE'));
    closeEditor(false); closeGroupEditor(false); el('notice').textContent = 'Removed group ' + g.name + '. Its sites are now independent.';
  } catch (e) { el('sites-error').textContent = e.message; }
  finally { setSiteBusy(false); el('add-group').focus(); }
}
optionalFields();
