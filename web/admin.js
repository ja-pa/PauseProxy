
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
const draftFields = ['preset', 'domain', 'domains', 'minutes', 'mode', 'enabled', 'site-group', 'budget-mode',
  'grayscale-enabled', 'grayscale-minutes', 'extra-enabled', 'extra-minutes', 'extra-uses', 'preset-bypass'];
const validatedFields = ['domain', 'domains', 'minutes', 'grayscale-minutes', 'extra-minutes', 'extra-uses', 'preset-bypass'];

function signedOut(message = '') {
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
  el('sites').replaceChildren();
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
    const actions = document.createElement('div'); actions.className = 'row-actions'; cell(tr, '').append(actions);
    const editButton = iconButton(actions, '✎', 'Edit ' + s.domain, b => openEditor(s, b));
    editButton.setAttribute('aria-controls', 'site-editor');
    editButton.setAttribute('aria-expanded', String(!el('site-editor').hidden && editing === s.domain));
    iconButton(actions, s.enabled ? '⏸' : '▶', (s.enabled ? 'Pause ' : 'Enable ') + s.domain, () => siteAction(s, 'PATCH'));
    iconButton(actions, '🗑', 'Remove ' + s.domain, () => siteAction(s, 'DELETE'), true);
    el('sites').append(tr);
  }
  if (!rules.sites.length) {
    const tr = document.createElement('tr'), empty = cell(tr, 'No sites yet. Use “+ Add site” to create your first rule.');
    empty.colSpan = 5; empty.className = 'empty-state'; el('sites').append(tr);
  }
}
// Mutation responses contain the full document. Apply it without a second fetch
// that could fail after a successful save, preserving a Media exceptions draft.
function applyRules(next, updateBypass = false) {
  const pristine = el('bypass').value === rules.bypass_domains.join('\n');
  rules = {...next, groups: next.groups || []}; drawSites(); drawGroups(); groupChoices();
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
async function refresh() {
  const d = await api('/api/state'); if (!password) return;
  drawOverview(d);
  const g = d.diagnostics;
  el('diagnostics').textContent = `Instance: ${d.instance_id} · sessions: ${d.sessions} · HTML injections: ${g.html_injections} · failures: ${g.injection_failures} · sessions created: ${g.sessions_created} · heartbeats: ${g.heartbeats_received}`;
  el('overview-error').textContent = '';
}
function drawOverview(d) {
  el('overview-date').textContent = d.date;
  el('overview-date').setAttribute('datetime', d.date);
  el('meta').textContent = 'Daily budgets for all configured users and sites.';
  const open = new Map([...el('rows').querySelectorAll('details')].map(n => [n.dataset.key, n.open]));
  const focused = document.activeElement?.dataset?.groupSummary;
  el('rows').replaceChildren();
  const groups = d.group_overview || [];
  const users = [...new Set([...d.overview.map(r => r.user), ...groups.map(g => g.user)])].sort();
  for (const user of users) {
    for (const group of groups.filter(g => g.user === user)) {
      const key = JSON.stringify([user, group.group_id]);
      const tr = document.createElement('tr'); tr.className = 'group-budget-row';
      cell(tr, user); cell(tr, group.name); fillBudget(cell(tr, ''), {...group, site: 'group ' + group.name}); el('rows').append(tr);
      const detailRow = document.createElement('tr'); detailRow.className = 'group-detail-row';
      const td = cell(detailRow, ''); td.colSpan = 3;
      const details = document.createElement('details'); details.dataset.key = key; details.open = open.get(key) || false;
      const members = d.overview.filter(r => r.user === user && r.group_id === group.group_id);
      const summary = document.createElement('summary'); summary.dataset.groupSummary = key;
      summary.textContent = `${members.length} site${members.length === 1 ? '' : 's'} in ${group.name}`;
      summary.setAttribute('aria-label', `Show sites in ${group.name} for ${user}`); details.append(summary);
      for (const row of members) {
        const member = document.createElement('section'); member.className = 'group-member';
        const name = document.createElement('strong'); name.textContent = row.site;
        const mode = document.createElement('small'); mode.textContent = budgetModeLabel(row.budget_mode) + ' · ' + duration(row.used_seconds) + ' used today';
        member.append(name, mode);
        if (row.budget_mode === 'shared') {
          if (budgetStateLabels[row.state]) { const label = document.createElement('span'); label.className = 'budget-status ' + row.state; label.textContent = budgetStateLabels[row.state]; member.append(label); }
        } else {
          if (row.budget_mode === 'capped') { const cap = document.createElement('small'); cap.textContent = 'Site cap including granted extra time: ' + duration(row.site_budget_seconds); member.append(cap); }
          fillBudget(member, row);
        }
        details.append(member);
      }
      td.append(details); el('rows').append(detailRow);
      if (focused === key) summary.focus({preventScroll: true});
    }
    for (const row of d.overview.filter(r => r.user === user && !r.group_id)) {
      const tr = document.createElement('tr'); cell(tr, row.user); cell(tr, row.site); fillBudget(cell(tr, ''), row); el('rows').append(tr);
    }
  }
  if (!d.overview.length && !groups.length) {
    const tr = document.createElement('tr');
    cell(tr, 'No sites configured. Add a site in Sites & limits to see its daily budget.').colSpan = 3; el('rows').append(tr);
  }
}
const budgetStateLabels = {paused: 'Paused', always_blocked: 'Always blocked', scheduled_break: 'Scheduled break', limit_reached: 'Daily limit reached', group_limit_reached: 'Group limit reached'};
function fillBudget(remaining, row) {
    remaining.className += ' budget-cell';
    if (row.state !== 'always_blocked') {
      const budget = Math.max(0, row.budget_seconds);
      const seconds = Math.max(0, Math.min(budget, row.remaining_seconds));
      const fraction = budget > 0 ? seconds / budget : 0;
      const text = `${duration(seconds)} left of ${duration(budget)}`;
      const label = document.createElement('span'); label.className = 'budget-text'; label.textContent = text;
      const progress = document.createElement('progress');
      progress.max = budget || 1; progress.value = seconds;
      progress.className = 'budget-progress ' + (row.state === 'paused' ? 'paused' : seconds <= 0 ? 'exhausted' : fraction <= 0.2 ? 'low' : 'available');
      progress.setAttribute('aria-label', `Time remaining for ${row.user} on ${row.site}`);
      progress.setAttribute('aria-valuetext', (budgetStateLabels[row.state] ? budgetStateLabels[row.state] + '. ' : '') + text);
      progress.title = text; remaining.append(label, progress);
    }
    if (budgetStateLabels[row.state]) {
      const status = document.createElement('span');
      status.className = 'budget-status ' + row.state; status.textContent = budgetStateLabels[row.state]; remaining.append(status);
    }
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
function optionalFields() {
  const limit = el('mode').value === 'limit';
  const shared = !!el('site-group').value && el('budget-mode').value === 'shared';
  el('budget-mode-field').hidden = !el('site-group').value;
  el('budget-help').textContent = !el('site-group').value ? 'This site has its own daily budget.' : {
    shared: 'This site spends the shared group budget. It has no separate time limit.',
    capped: 'This site spends group time and has its own cap. Whichever runs out first blocks access. Extra time cannot bypass the group limit.',
    independent: 'This site is listed in the group but spends only its own budget.'
  }[el('budget-mode').value];
  el('minutes-field').hidden = !limit || shared; el('optional-limits').hidden = !limit; el('minutes').disabled = !limit || shared;
  el('shared-extra-help').hidden = !shared;
  el('grayscale-help').textContent = shared ? "Measured against this site's own active time, not the group's total." : 'Must be earlier than the daily budget.';
  el('access-help').textContent = limit ? "Active time is shared across the site's domains and browser tabs."
    : 'This site is blocked whenever the rule is enabled.';
  for (const [toggle, fields] of [['grayscale-enabled', 'grayscale-fields'], ['extra-enabled', 'extra-fields']]) {
    const active = limit && el(toggle).checked && !(shared && toggle === 'extra-enabled');
    el(toggle).disabled = !limit || (shared && toggle === 'extra-enabled'); el(toggle).setAttribute('aria-expanded', String(active)); el(fields).hidden = !active;
  }
  el('grayscale-minutes').disabled = !limit || !el('grayscale-enabled').checked;
  el('extra-minutes').disabled = el('extra-uses').disabled = !limit || shared || !el('extra-enabled').checked;
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
  el('mode').value = s?.mode || 'limit'; el('enabled').checked = s ? s.enabled : true;
  groupChoices(); el('site-group').value = s?.group_id || ''; el('budget-mode').value = s?.budget_mode || 'shared';
  el('grayscale-enabled').checked = !!s?.grayscale_enabled;
  el('grayscale-minutes').value = s?.grayscale_after_seconds ? s.grayscale_after_seconds / 60 : '';
  el('extra-enabled').checked = !!s?.extra_time_enabled;
  el('extra-minutes').value = (s?.extra_time_seconds || 180) / 60; el('extra-uses').value = s?.extra_time_max_uses || 1;
  el('preset-bypass').value = ''; el('preset-bypass').disabled = !!s; el('preset-bypass-field').hidden = !!s;
  el('preset-note').textContent = ''; el('advanced-domains').open = false;
  optionalFields(); updateDomainSummary(); el('site-editor').hidden = false;
  el('add-site').setAttribute('aria-expanded', String(!s)); el('sites-error').textContent = ''; el('notice').textContent = '';
  editorBaseline = draftSnapshot(); drawSites(); focusEditor();
}
function focusEditor() {
  el('site-editor').scrollIntoView({block: 'start'}); el(editing ? 'mode' : 'preset').focus({preventScroll: true});
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
  siteBusy = busy; el('editor-fields').disabled = busy; el('site-form').setAttribute('aria-busy', String(busy));
  el('group-fields').disabled = busy; el('add-group').disabled = busy;
  el('add-site').disabled = el('logout').disabled = busy;
  for (const b of el('sites').querySelectorAll('button')) b.disabled = busy;
  for (const b of el('groups').querySelectorAll('button')) b.disabled = busy;
}
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
  const limit = el('mode').value === 'limit';
  const shared = !!el('site-group').value && el('budget-mode').value === 'shared';
  let seconds = Math.round(Number(el('minutes').value) * 60);
  if (limit && !shared && !validSeconds(seconds)) return fieldError('minutes', 'Use a daily budget between 1 second and 1440 minutes.');
  if (!validSeconds(seconds)) seconds = 900;
  const gray = limit && el('grayscale-enabled').checked, graySeconds = Math.round(Number(el('grayscale-minutes').value) * 60);
  if (gray && (!Number.isInteger(graySeconds) || graySeconds < 1 || graySeconds > 86400 || (!shared && graySeconds >= seconds)))
    return fieldError('grayscale-minutes', shared ? 'Use a grayscale threshold between 1 second and 1440 minutes.' : 'Start grayscale at least 1 second after zero and before the daily budget.');
  const extra = limit && !shared && el('extra-enabled').checked;
  const extraSeconds = Math.round(Number(el('extra-minutes').value) * 60), extraUses = Number(el('extra-uses').value);
  if (extra && !validSeconds(extraSeconds)) return fieldError('extra-minutes', 'Use between 1 second and 1440 minutes per extension.');
  if (extra && (!Number.isInteger(extraUses) || extraUses < 1 || extraUses > 100)) return fieldError('extra-uses', 'Use a whole number from 1 to 100.');
  const body = {domains: targets, enabled: el('enabled').checked, mode: el('mode').value, daily_limit_seconds: seconds,
    group_id: el('site-group').value, budget_mode: el('site-group').value ? el('budget-mode').value : 'independent',
    grayscale_enabled: gray, grayscale_after_seconds: Number.isInteger(graySeconds) && graySeconds >= 0 ? graySeconds : 0,
    extra_time_enabled: extra, extra_time_seconds: validSeconds(extraSeconds) ? extraSeconds : 180,
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
el('grayscale-enabled').onchange = () => {
  if (el('grayscale-enabled').checked && !el('grayscale-minutes').value)
    el('grayscale-minutes').value = Math.max(1, Math.floor(Number(el('minutes').value) * 60 * 2 / 3) || 1) / 60;
  clearErrors(); optionalFields();
};
el('extra-enabled').onchange = el('mode').onchange = () => { clearErrors(); optionalFields(); };
el('site-group').onchange = () => { if (el('site-group').value && !el('budget-mode').value) el('budget-mode').value = 'shared'; clearErrors(); optionalFields(); };
el('budget-mode').onchange = () => { clearErrors(); optionalFields(); };
window.addEventListener('beforeunload', e => { if (password && isDirty()) { e.preventDefault(); e.returnValue = ''; } });
el('login-form').onsubmit = async e => {
  e.preventDefault(); clearInterval(polling); password = el('password').value; el('connect').disabled = true;
  try {
    await refreshRules(true); await refresh();
    el('password').value = ''; el('login').hidden = true; el('settings').hidden = false; el('logout').hidden = false; el('error').textContent = '';
    polling = setInterval(() => refresh().catch(e => el('overview-error').textContent = e.message), 2000);
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
  if (!confirm('Reset all in-memory statistics? Saved rules will stay unchanged.')) return;
  try { await api('/api/reset', 'POST', {}); await refresh(); } catch (e) { el('overview-error').textContent = e.message; }
};
fetch('/connection').then(r => { if (!r.ok) throw Error('Could not load connection details'); return r.json(); }).then(c => {
  el('pac').textContent = c.pac; el('pac-link').href = c.pac; el('ca').href = c.ca; el('demo').href = c.demo;
}).catch(e => el('connection-error').textContent = e.message);
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
  const adjustGray = members.some(s => s.budget_mode === 'shared' && s.grayscale_enabled && s.grayscale_after_seconds >= g.daily_limit_seconds);
  if (!confirm(`Remove group ${g.name}? Its ${members.length} site(s) will keep independent budgets. Shared sites inherit ${duration(g.daily_limit_seconds)}. Today's usage stays recorded.` +
    (adjustGray ? ' Grayscale will be turned off where its threshold does not fit the inherited site limit; the threshold value stays saved.' : ''))) return;
  if (!discardAllowed()) return;
  setSiteBusy(true); el('sites-error').textContent = '';
  try {
    applyRules(await api('/api/groups/' + encodeURIComponent(g.id), 'DELETE'));
    closeEditor(false); closeGroupEditor(false); el('notice').textContent = 'Removed group ' + g.name + '. Its sites are now independent.';
  } catch (e) { el('sites-error').textContent = e.message; }
  finally { setSiteBusy(false); el('add-group').focus(); }
}
optionalFields();
