const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');

// Exercise the actual admin script and HTML defaults with a small DOM fixture.
// Real browser layout, focus and form submission are checked separately.
function fixture(initialSites = []) {
  const nodes = new Map(), requests = [], dialogs = [], events = new Map();
  let activeElement, allowDiscard = true, failure = '', pending, snapshot;
  let stored = {sites: structuredClone(initialSites), groups: [], bypass_domains: []};
  function node(tag, attrs = {}) {
    const listeners = new Map();
    const n = {tag, attrs, children: [], dataset: {}, hidden: 'hidden' in attrs,
      disabled: 'disabled' in attrs, checked: 'checked' in attrs, value: attrs.value || '',
      type: attrs.type || '', textContent: '', className: '', isConnected: true,
      append(...children) { this.children.push(...children); },
      replaceChildren(...children) { this.children = children; },
      setAttribute(k, v) { this.attrs[k] = String(v); },
      getAttribute(k) { return this.attrs[k] ?? null; },
      removeAttribute(k) { delete this.attrs[k]; },
      addEventListener(k, fn) { listeners.set(k, fn); },
      focus() { activeElement = this; }, scrollIntoView() {},
      querySelectorAll(selector) {
        return this.children.flatMap(c => [...(selector === c.tag ? [c] : []), ...c.querySelectorAll(selector)]);
      }
    };
    let value = n.value;
    Object.defineProperty(n, 'value', {get: () => value, set: v => { value = String(v); }});
    if (attrs['data-tab']) n.dataset.tab = attrs['data-tab'];
    return n;
  }
  const html = readFileSync(__dirname + '/admin.html', 'utf8');
  for (const match of html.matchAll(/<([a-z][a-z0-9]*)\b([^>]*)>/g)) {
    const attrs = Object.fromEntries([...match[2].matchAll(/([\w-]+)(?:="([^"]*)")?/g)].map(a => [a[1], a[2] ?? '']));
    if (attrs.id) nodes.set(attrs.id, node(match[1], attrs));
  }
  const get = id => nodes.get(id);
  get('mode').value = 'limit'; get('preset').value = '';
  const formHTML = html.slice(html.indexOf('<form id="site-form"')).split('</form>')[0];
  const formIds = new Set([...formHTML.matchAll(/\bid="([^"]+)"/g)].map(m => m[1]));
  const defaults = [...nodes.values()].filter(n => formIds.has(n.attrs.id)).map(n => [n, n.value, n.checked]);
  get('site-form').reset = () => { for (const [n, value, checked] of defaults) { n.value = value; n.checked = checked; } };
  const tabs = [...nodes.values()].filter(n => n.attrs.role === 'tab');
  const ctx = vm.createContext({URL, console,
    document: {getElementById: get, createElement: tag => node(tag), querySelectorAll: () => tabs},
    window: {addEventListener: (name, fn) => events.set(name, fn)},
    location: {hash: '#sites'},
    setInterval() { return 1; }, clearInterval() {},
    confirm(message) { dialogs.push(message); return allowDiscard; },
    fetch: async (path, options = {}) => {
      if (path === '/connection') return {ok: true, json: async () => ({pac: '/pac', ca: '/ca', demo: '/demo'})};
      const method = options.method || 'GET';
      const body = options.body ? JSON.parse(options.body) : undefined;
      requests.push({path, method, body});
      if (pending) await pending;
      if (failure) return {ok: false, status: 400, json: async () => ({error: failure})};
      if (path === '/api/state') return {ok: true, status: 200, json: async () => structuredClone(snapshot)};
      if (path.startsWith('/api/groups')) {
        if (method === 'POST') stored.groups.push({id: 'group-' + (stored.groups.length + 1), ...body});
        else if (method === 'PATCH') Object.assign(stored.groups.find(g => g.id === path.split('/').pop()), body);
        else if (method === 'DELETE') stored.groups = stored.groups.filter(g => g.id !== path.split('/').pop());
        return {ok: true, status: 200, json: async () => structuredClone(stored)};
      }
      if (method === 'POST') {
        const {bypass_domains = [], ...site} = body;
        stored.sites.push(site); stored.bypass_domains.push(...bypass_domains);
      } else if (method === 'PATCH') {
        Object.assign(stored.sites.find(s => s.domain === decodeURIComponent(path.split('/').pop())), body);
      } else if (method === 'DELETE') {
        stored.sites = stored.sites.filter(s => s.domain !== decodeURIComponent(path.split('/').pop()));
      }
      return {ok: true, status: 200, json: async () => structuredClone(stored)};
    }
  });
  vm.runInContext(readFileSync(__dirname + '/admin.js', 'utf8'), ctx);
  ctx.initialRules = structuredClone(stored);
  vm.runInContext("password = 'test'; applyRules(initialRules)", ctx);
  const run = code => vm.runInContext(code, ctx);
  return {get, requests, dialogs, run, events,
    focus: () => activeElement,
    submit: () => get('site-form').onsubmit({preventDefault() {}}),
    allowDiscard(value) { allowDiscard = value; }, fail(message) { failure = message; },
    setSnapshot(value) { snapshot = value; },
    hold() { let release; pending = new Promise(resolve => { release = resolve; }); return () => { pending = null; release(); }; }
  };
}
const site = (domain = 'x.com') => ({domain, domains: [domain, 'www.' + domain], enabled: true,
  mode: 'limit', daily_limit_seconds: 900, grayscale_enabled: false, grayscale_after_seconds: 0,
  extra_time_enabled: false, extra_time_seconds: 180, extra_time_max_uses: 1});

test('new site is hidden until requested; defaults, Enter handler and successful save', async () => {
  const f = fixture();
  assert.equal(f.get('site-editor').hidden, true);
  f.get('add-site').onclick();
  assert.equal(f.get('site-editor').hidden, false);
  assert.equal(f.focus(), f.get('preset'));
  assert.equal(f.get('minutes').value, '15');
  assert.equal(f.get('grayscale-enabled').checked, false);
  assert.equal(f.get('extra-enabled').checked, false);
  assert.equal(f.get('extra-minutes').value, '3');
  assert.equal(f.get('extra-uses').value, '1');
  f.get('domain').value = 'example.com'; f.get('domain').onchange();
  assert.equal(f.get('domains').value, 'example.com\nwww.example.com');
  await f.submit();
  assert.equal(f.requests.length, 1);
  assert.equal(f.requests[0].method, 'POST');
  assert.equal(f.requests[0].body.daily_limit_seconds, 900);
  assert.equal(f.get('site-editor').hidden, true);
  assert.match(f.get('notice').textContent, /Saved example.com/);
});

test('YouTube preset keeps all exact domains and proposed media exceptions on save', async () => {
  const f = fixture(); f.get('add-site').onclick(); f.get('preset').value = 'youtube'; f.get('preset').onchange();
  assert.match(f.get('domains-summary').textContent, /4 target domains/);
  assert.match(f.get('domains-summary').textContent, /2 media exceptions/);
  await f.submit();
  assert.deepEqual(f.requests[0].body.domains, ['youtube.com', 'www.youtube.com', 'm.youtube.com', 'youtu.be']);
  assert.deepEqual(f.requests[0].body.bypass_domains, ['googlevideo.com', 'ytimg.com']);
});

test('optional fields follow toggles; switching access mode keeps the draft and block saves valid hidden defaults', async () => {
  const f = fixture(); f.get('add-site').onclick(); f.get('domain').value = 'blocked.example';
  f.get('grayscale-enabled').checked = true; f.get('grayscale-enabled').onchange();
  assert.equal(f.get('grayscale-minutes').value, '10');
  assert.equal(f.get('grayscale-fields').hidden, false);
  f.get('extra-enabled').checked = true; f.get('extra-enabled').onchange();
  f.get('extra-minutes').value = '7';
  f.get('mode').value = 'block'; f.get('mode').onchange();
  assert.equal(f.get('optional-limits').hidden, true);
  f.get('mode').value = 'limit'; f.get('mode').onchange();
  assert.equal(f.get('grayscale-minutes').value, '10'); assert.equal(f.get('extra-minutes').value, '7');
  f.get('mode').value = 'block'; f.get('mode').onchange();
  f.get('minutes').value = ''; f.get('extra-minutes').value = '';
  await f.submit();
  const body = f.requests[0].body;
  assert.equal(body.mode, 'block'); assert.equal(body.daily_limit_seconds, 900);
  assert.equal(body.grayscale_enabled, false); assert.equal(body.extra_time_enabled, false);
  assert.equal(body.extra_time_seconds, 180);
});

test('cancel and switching rules protect drafts; changing tabs keeps them', () => {
  const f = fixture([site(), site('other.example')]);
  f.run('openEditor(rules.sites[0])'); f.get('minutes').value = '20'; f.allowDiscard(false);
  f.get('cancel').onclick(); assert.equal(f.get('site-editor').hidden, false);
  f.run('openEditor(rules.sites[1])'); assert.equal(f.get('edit-title').textContent, 'Edit x.com');
  f.get('tab-media').onclick(); f.get('tab-sites').onclick(); assert.equal(f.get('minutes').value, '20');
  f.allowDiscard(true); f.get('cancel').onclick(); assert.equal(f.get('site-editor').hidden, true);
  assert.equal(f.requests.length, 0); assert.equal(f.dialogs.length, 3);
});

test('inline validation opens advanced fields or focuses the invalid numeric field without sending', async () => {
  const f = fixture(); f.get('add-site').onclick(); f.get('domain').value = 'example.com';
  f.get('domains').value = 'other.example'; await f.submit();
  assert.equal(f.get('advanced-domains').open, true); assert.equal(f.focus(), f.get('domains'));
  f.get('domains').value = 'example.com'; f.get('minutes').value = '0'; await f.submit();
  assert.equal(f.focus(), f.get('minutes')); assert.equal(f.get('minutes').getAttribute('aria-invalid'), 'true');
  f.get('minutes').value = '15'; f.get('extra-enabled').checked = true; f.get('extra-uses').value = '1.5';
  await f.submit(); assert.equal(f.focus(), f.get('extra-uses')); assert.equal(f.requests.length, 0);
});

test('failed save preserves draft; an in-flight request blocks duplicate submissions and row actions', async () => {
  const f = fixture([site()]); f.get('add-site').onclick(); f.get('domain').value = 'example.com';
  f.fail('Could not save rules'); await f.submit();
  assert.equal(f.get('site-editor').hidden, false); assert.equal(f.get('domain').value, 'example.com');
  assert.equal(f.get('form-error').textContent, 'Could not save rules');
  assert.equal(f.get('editor-fields').disabled, false);
  f.fail(''); const release = f.hold(); const saving = f.submit();
  assert.equal(f.get('editor-fields').disabled, true);
  await f.submit(); await f.run("siteAction(rules.sites[0], 'DELETE')");
  assert.equal(f.requests.length, 2); release(); await saving;
  assert.equal(f.get('site-editor').hidden, true);
});

test('list actions have accessible domain labels, pause/enable patch correctly, and deletion requires confirmation', async () => {
  const f = fixture([site()]);
  const buttons = f.get('sites').querySelectorAll('button');
  assert.deepEqual(buttons.map(b => b.getAttribute('aria-label')), ['Edit x.com', 'Pause x.com', 'Remove x.com']);
  await buttons[1].onclick(); assert.deepEqual(f.requests[0].body, {enabled: false});
  const enable = f.get('sites').querySelectorAll('button')[1]; assert.equal(enable.title, 'Enable x.com');
  await enable.onclick(); assert.deepEqual(f.requests[1].body, {enabled: true});
  f.allowDiscard(false); await f.get('sites').querySelectorAll('button')[2].onclick(); assert.equal(f.requests.length, 2);
  assert.match(f.dialogs[0], /Remove x.com/);
  f.allowDiscard(true); await f.get('sites').querySelectorAll('button')[2].onclick();
  assert.equal(f.requests[2].method, 'DELETE'); assert.equal(f.get('sites').children[0].children[0].colSpan, 5);
});

test('updating a site uses PATCH, preserves a draft in Media exceptions, and block rows hide the limit', async () => {
  const blocked = {...site(), mode: 'block'}; const f = fixture([blocked]);
  assert.equal(f.get('sites').children[0].children[1].textContent, '—');
  f.get('bypass').value = 'draft.example'; f.run('openEditor(rules.sites[0])');
  assert.equal(f.get('domain-field').hidden, true); assert.equal(f.get('preset-bypass-field').hidden, true);
  await f.submit();
  assert.equal(f.requests[0].method, 'PATCH'); assert.equal(f.requests[0].path, '/api/sites/x.com');
  assert.equal('bypass_domains' in f.requests[0].body, false); assert.equal(f.get('bypass').value, 'draft.example');
});

const overviewRow = (values = {}) => ({user: 'dev', site: 'x.com', used_seconds: 0,
  budget_seconds: 900, remaining_seconds: 900, state: 'available', ...values});
const overviewData = (rows) => ({date: '2026-10-15', overview: rows, sessions: 0,
  instance_id: 'test', diagnostics: {html_injections: 0, injection_failures: 0, sessions_created: 0, heartbeats_received: 0}});

test('overview renders remaining budget, exact time and accessible progress labels instead of counters', async () => {
  const f = fixture(); f.setSnapshot(overviewData([overviewRow({remaining_seconds: 754, used_seconds: 146})]));
  await f.run('refresh()');
  assert.equal(f.get('overview-date').textContent, '2026-10-15');
  assert.equal(f.get('overview-date').getAttribute('datetime'), '2026-10-15');
  const cells = f.get('rows').children[0].children;
  assert.equal(cells.length, 3); assert.equal(cells[0].textContent, 'dev'); assert.equal(cells[1].textContent, 'x.com');
  const [label, progress] = cells[2].children;
  assert.equal(label.textContent, '13 min left of 15 min');
  assert.equal(progress.max, 900); assert.equal(progress.value, '754');
  assert.equal(progress.getAttribute('aria-label'), 'Time remaining for dev on x.com');
  assert.equal(progress.getAttribute('aria-valuetext'), label.textContent);
  assert.match(progress.className, /available/);
});

test('overview marks the 20 percent boundary and zero budgets and clamps progress values', async () => {
  const f = fixture();
  f.setSnapshot(overviewData([overviewRow({remaining_seconds: 181}), overviewRow({remaining_seconds: 180}),
    overviewRow({remaining_seconds: 0, state: 'limit_reached'}), overviewRow({remaining_seconds: -50}),
    overviewRow({remaining_seconds: 1000})]));
  await f.run('refresh()');
  const budgets = f.get('rows').children.map(tr => tr.children[2].children);
  assert.match(budgets[0][1].className, /available/); assert.match(budgets[1][1].className, /low/);
  assert.match(budgets[2][1].className, /exhausted/); assert.equal(budgets[2][2].textContent, 'Daily limit reached');
  assert.equal(budgets[3][1].value, '0'); assert.equal(budgets[4][1].value, '900');
});

test('overview distinguishes paused, always-blocked and scheduled rules and displays bonus budgets', async () => {
  const f = fixture();
  f.setSnapshot(overviewData([overviewRow({state: 'paused'}), overviewRow({state: 'always_blocked'}),
    overviewRow({state: 'scheduled_break', remaining_seconds: 300}),
    overviewRow({budget_seconds: 1080, remaining_seconds: 180, used_seconds: 900})]));
  await f.run('refresh()');
  const budgets = f.get('rows').children.map(tr => tr.children[2].children);
  assert.match(budgets[0][1].className, /paused/); assert.equal(budgets[0][2].textContent, 'Paused');
  assert.equal(budgets[1].length, 1); assert.equal(budgets[1][0].textContent, 'Always blocked');
  assert.equal(budgets[2][1].value, '300'); assert.equal(budgets[2][2].textContent, 'Scheduled break');
  assert.equal(budgets[3][0].textContent, '3 min left of 18 min');
});

test('overview time labels use minutes and hours without seconds', () => {
  const f = fixture();
  for (const [seconds, expected] of [[0, '0 min'], [30, '<1 min'], [60, '1 min'],
    [754, '13 min'], [3600, '1 h'], [3900, '1 h 5 min'], [7200, '2 h']]) {
    assert.equal(f.run(`duration(${seconds})`), expected);
  }
});

test('overview empty state spans three columns; failed refresh preserves the previous view', async () => {
  const f = fixture(); f.setSnapshot(overviewData([])); await f.run('refresh()');
  assert.equal(f.get('rows').children[0].children[0].colSpan, 3);
  assert.match(f.get('rows').children[0].children[0].textContent, /No sites configured/);
  f.setSnapshot(overviewData([overviewRow()])); await f.run('refresh()');
  const before = f.get('rows').children[0]; f.fail('Offline');
  await assert.rejects(f.run('refresh()'), /Offline/);
  assert.equal(f.get('rows').children[0], before);
});

test('group editor defaults, validation, save, rename, dirty guard and failed mutation', async () => {
  const f = fixture(); f.get('add-group').onclick();
  assert.equal(f.get('group-editor').hidden, false); assert.equal(f.get('group-minutes').value, '15');
  assert.equal(f.focus(), f.get('group-name'));
  await f.get('group-form').onsubmit({preventDefault(){}});
  assert.equal(f.get('group-name').getAttribute('aria-invalid'), 'true');
  f.get('group-name').value = 'Fun'; f.allowDiscard(false); f.get('add-site').onclick();
  assert.equal(f.get('site-editor').hidden, true);
  await f.get('group-form').onsubmit({preventDefault(){}});
  assert.equal(f.requests[0].path, '/api/groups'); assert.deepEqual(f.requests[0].body,{name:'Fun',daily_limit_seconds:900});
  assert.equal(f.get('group-editor').hidden,true);
  f.run('openGroupEditor(rules.groups[0])'); f.get('group-name').value='Leisure';
  f.fail('Could not write rules'); await f.get('group-form').onsubmit({preventDefault(){}});
  assert.equal(f.get('group-name').value,'Leisure'); assert.equal(f.get('group-error').textContent,'Could not write rules');
  f.fail(''); await f.get('group-form').onsubmit({preventDefault(){}});
  assert.equal(f.requests.at(-1).method,'PATCH'); assert.equal(f.get('group-editor').hidden,true);
});

test('site budget modes preserve draft values and suppress extras only while sharing', async () => {
  const f = fixture(); f.get('add-group').onclick();f.get('group-name').value='Fun';
  await f.get('group-form').onsubmit({preventDefault(){}});
  f.get('add-site').onclick(); f.get('domain').value='example.com';
  f.get('minutes').value='7'; f.get('extra-enabled').checked=true;
  f.get('site-group').value='group-1'; f.get('budget-mode').value='shared'; f.get('budget-mode').onchange();
  assert.equal(f.get('minutes-field').hidden,true); assert.equal(f.get('extra-enabled').disabled,true);
  assert.equal(f.get('shared-extra-help').hidden,false);
  f.get('budget-mode').value='capped'; f.get('budget-mode').onchange();
  assert.equal(f.get('minutes').value,'7'); assert.equal(f.get('extra-enabled').checked,true);
  assert.equal(f.get('extra-enabled').disabled,false);
  await f.submit(); assert.equal(f.requests.at(-1).body.budget_mode,'capped');
  assert.equal(f.requests.at(-1).body.group_id,'group-1');
});

test('group overview shows one shared bar, member usage and separate independent budgets and retains expansion', async () => {
  const f = fixture();
  const data=overviewData([
    overviewRow({group_id:'fun',budget_mode:'shared',used_seconds:60}),
    overviewRow({site:'youtube.com',group_id:'fun',budget_mode:'capped',site_budget_seconds:300}),
    overviewRow({site:'independent.example',group_id:'fun',budget_mode:'independent'})
  ]);
  data.group_overview=[{user:'dev',group_id:'fun',name:'Fun',budget_seconds:1800,remaining_seconds:1740,state:'available'}];
  f.setSnapshot(data); await f.run('refresh()');
  assert.equal(f.get('rows').children.length,2);
  const details=f.get('rows').querySelectorAll('details')[0];
  assert.equal(details.open,false); assert.match(details.children[0].textContent,/3 sites in Fun/);
  assert.equal(f.get('rows').querySelectorAll('progress').length,3);
  assert.match(details.children[1].children[1].textContent,/1 min used today/);
  details.open=true; await f.run('refresh()');
  assert.equal(f.get('rows').querySelectorAll('details')[0].open,true);
});
