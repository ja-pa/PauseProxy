const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');

// Exercise the actual admin script and HTML defaults with a small DOM fixture.
// Real browser layout, focus and form submission are checked separately.
function fixture(initialSites = []) {
  const nodes = new Map(), requests = [], dialogs = [], events = new Map(), downloads = [], blobs = [];
  let activeElement, allowDiscard = true, failure = '', pending, snapshot;
  let stored = {sites: structuredClone(initialSites), groups: [], bypass_domains: []};
  function node(tag, attrs = {}) {
    const listeners = new Map();
    const n = {tag, attrs, children: [], dataset: {}, hidden: 'hidden' in attrs,
      disabled: 'disabled' in attrs, checked: 'checked' in attrs, value: attrs.value || '',
      open: false, style: {}, type: attrs.type || '', textContent: '', className: '', isConnected: true,
      append(...children) { for(const c of children){c.remove();c.parent=this;this.children.push(c);} },
      remove() {if(this.parent){this.parent.children=this.parent.children.filter(c=>c!==this);this.parent=null;}},
      insertBefore(c,before) {c.remove();c.parent=this;const i=before?this.children.indexOf(before):this.children.length;this.children.splice(i,0,c);},
      replaceChildren(...children) {for(const c of this.children)c.parent=null;this.children=[];this.append(...children);},
      setAttribute(k, v) { this.attrs[k] = String(v); },
      getAttribute(k) { return this.attrs[k] ?? null; },
      removeAttribute(k) { delete this.attrs[k]; },
      addEventListener(k, fn) { if (!listeners.has(k)) listeners.set(k, []); listeners.get(k).push(fn); },
      emit(k) { for (const fn of listeners.get(k) || []) fn(); },
      focus() { activeElement = this; }, scrollIntoView() {},
      click() { if (tag === 'a') downloads.push({href: this.href, download: this.download}); if (this.onclick) return this.onclick(); },
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
  get('preset').value = ''; get('extra-method').value = 'click';
  const formHTML = html.slice(html.indexOf('<form id="site-form"')).split('</form>')[0];
  const formIds = new Set([...formHTML.matchAll(/\bid="([^"]+)"/g)].map(m => m[1]));
  const defaults = [...nodes.values()].filter(n => formIds.has(n.attrs.id)).map(n => [n, n.value, n.checked]);
  get('site-form').reset = () => { for (const [n, value, checked] of defaults) { n.value = value; n.checked = checked; } };
  const tabs = [...nodes.values()].filter(n => n.attrs.role === 'tab');
  class DownloadURL extends URL {
    static createObjectURL(blob) {blobs.push(blob); return 'blob:test';}
    static revokeObjectURL() {}
  }
  const ctx = vm.createContext({URL: DownloadURL, Blob, console, setTimeout,
    document: {body: node('body'), getElementById: get, createElement: tag => node(tag), querySelectorAll: () => tabs},
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
      if (path.endsWith('/reset')) return {ok:true,status:200,json:async()=>({ok:true})};
      if (path.startsWith('/api/groups')) {
        if (method === 'POST') stored.groups.push({id: 'group-' + (stored.groups.length + 1), ...body});
        else if (method === 'PATCH') Object.assign(stored.groups.find(g => g.id === path.split('/').pop()), body);
        else if (method === 'DELETE') stored.groups = stored.groups.filter(g => g.id !== path.split('/').pop());
        return {ok: true, status: 200, json: async () => structuredClone(stored)};
      }
      if (path === '/api/sites' && method === 'PUT') stored = structuredClone(body);
      else if (method === 'POST') {
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
  return {get, requests, dialogs, run, events, downloads, blobs,
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

const configDocument = () => ({version: 2, sites: [{...site('example.com'), group_id: 'fun', budget_mode: 'capped',
  blur_enabled: true, blur_after_seconds: 100, extra_time_method: 'reflection'}],
  groups: [{id: 'fun', name: 'Fun', daily_limit_seconds: 1800}], bypass_domains: ['media.example.com']});
function selectConfig(f, contents = JSON.stringify(configDocument()), size = Buffer.byteLength(contents)) {
  f.get('config-file').files = [{size, text: async () => contents}];
  f.get('config-file').value = 'selected.json';
  return f.get('config-file').onchange();
}

test('download reads saved rules without exporting drafts or credentials', async () => {
  const f = fixture([site()]); f.get('add-site').onclick(); f.get('domain').value = 'draft.example';
  await f.get('download-config').onclick();
  assert.deepEqual(f.downloads, [{href: 'blob:test', download: 'pauseproxy-sites.json'}]);
  const text = await f.blobs[0].text(), saved = JSON.parse(text);
  assert.equal(saved.sites[0].domain, 'x.com'); assert.ok(text.includes('\n  "sites"'));
  assert.ok(!text.includes('draft.example')); assert.ok(!text.includes('password'));
  assert.equal(f.get('domain').value, 'draft.example'); assert.equal(f.dialogs.length, 0);
  f.fail('offline'); await f.get('download-config').onclick();
  assert.match(f.get('config-error').textContent, /offline/); assert.equal(f.downloads.length, 1);
});

test('import replaces complete config, refreshes media and stats, and allows selecting same file again', async () => {
  const f = fixture([site()]); f.setSnapshot(overviewData([]));
  await selectConfig(f);
  assert.deepEqual(f.requests.find(r => r.method === 'PUT').body, configDocument());
  assert.equal(f.get('sites').children[0].children[0].children[0].textContent, 'example.com');
  assert.equal(f.get('bypass').value, 'media.example.com');
  assert.equal(f.get('site-editor').hidden, true); assert.equal(f.get('group-editor').hidden, true);
  assert.equal(f.get('config-file').value, ''); assert.match(f.get('notice').textContent, /Imported config/);
  assert.ok(f.requests.some(r => r.path === '/api/state'));
  await selectConfig(f); assert.equal(f.requests.filter(r => r.method === 'PUT').length, 2);
});

test('canceling import or draft discard preserves editors and sends no mutation', async () => {
  for (const draft of ['site', 'group', 'media']) {
    const f = fixture([site()]);
    if (draft === 'site') {f.get('add-site').onclick(); f.get('domain').value = 'draft.example';}
    if (draft === 'group') {f.get('add-group').onclick(); f.get('group-name').value = 'Draft';}
    if (draft === 'media') f.get('bypass').value = 'draft.example';
    f.allowDiscard(false); await selectConfig(f);
    assert.equal(f.requests.length, 0); assert.equal(f.get('config-file').value, '');
    // Accept replacement but decline the second, draft-specific confirmation.
    f.run('confirm = message => !message.startsWith("Discard")');
    await selectConfig(f); assert.equal(f.requests.length, 0);
    if (draft === 'site') {assert.equal(f.get('site-editor').hidden, false); assert.equal(f.get('domain').value, 'draft.example');}
    if (draft === 'group') {assert.equal(f.get('group-editor').hidden, false); assert.equal(f.get('group-name').value, 'Draft');}
    if (draft === 'media') assert.equal(f.get('bypass').value, 'draft.example');
  }
});

test('import handles bad files and server errors without losing draft or rules', async () => {
  const f = fixture([site()]); f.get('add-site').onclick(); f.get('domain').value = 'draft.example';
  await selectConfig(f, '{}', 1024*1024 + 1); assert.match(f.get('config-error').textContent, /1 MiB/);
  await selectConfig(f, '{'); assert.match(f.get('config-error').textContent, /valid JSON/);
  for (const text of ['null', '[]', '42']) {await selectConfig(f, text); assert.match(f.get('config-error').textContent, /JSON object/);}
  f.get('config-file').files = [{size: 1, text: async () => {throw Error('denied');}}];
  await f.get('config-file').onchange(); assert.match(f.get('config-error').textContent, /Could not read/);
  assert.equal(f.requests.length, 0);
  f.fail('invalid group reference'); await selectConfig(f);
  assert.match(f.get('config-error').textContent, /invalid group reference/);
  assert.equal(f.get('site-editor').hidden, false); assert.equal(f.get('domain').value, 'draft.example');
  assert.equal(f.run('rules.sites[0].domain'), 'x.com'); assert.equal(f.get('import-config').disabled, false);
});

test('file reading and importing disable concurrent mutations; refresh failure reports saved import', async () => {
  const f = fixture([site()]); let finish;
  f.get('config-file').files = [{size: 10, text: () => new Promise(resolve => {finish = resolve;})}];
  const pending = f.get('config-file').onchange();
  for (const id of ['download-config','import-config','add-site','add-group','save-bypass','reset','logout']) assert.equal(f.get(id).disabled, true);
  await f.get('reset').onclick(); await f.get('save-bypass').onclick(); assert.equal(f.requests.length, 0);
  finish(JSON.stringify(configDocument())); await pending;
  assert.match(f.get('notice').textContent, /Imported config/);
  assert.match(f.get('config-error').textContent, /imported, but refreshing statistics failed/);
  assert.equal(f.get('import-config').disabled, false);
});

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
  f.get('always-block').checked = true; f.get('always-block').onchange();
  assert.equal(f.get('optional-limits').hidden, true);
  f.get('always-block').checked = false; f.get('always-block').onchange();
  assert.equal(f.get('grayscale-minutes').value, '10'); assert.equal(f.get('extra-minutes').value, '7');
  f.get('always-block').checked = true; f.get('always-block').onchange();
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
  assert.deepEqual(buttons.map(b => b.getAttribute('aria-label')), ['Edit x.com', 'Pause x.com', 'Remove x.com', 'Reset statistics for x.com']);
  await buttons[1].onclick(); assert.deepEqual(f.requests[0].body, {enabled: false});
  const enable = f.get('sites').querySelectorAll('button')[1]; assert.equal(enable.title, 'Enable x.com');
  await enable.onclick(); assert.deepEqual(f.requests[1].body, {enabled: true});
  f.allowDiscard(false); await f.get('sites').querySelectorAll('button')[2].onclick(); assert.equal(f.requests.length, 2);
  assert.match(f.dialogs[0], /Remove x.com/);
  f.allowDiscard(true); await f.get('sites').querySelectorAll('button')[2].onclick();
  assert.equal(f.requests[2].method, 'DELETE'); assert.equal(f.get('sites').children[0].children[0].colSpan, 6);
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

test('overview renders a compact accessible timeline and uses remaining budget text', async () => {
 const f=fixture();f.setSnapshot(overviewData([overviewRow({remaining_seconds:754,used_seconds:146})]));await f.run('refresh()');
 const cells=f.get('rows').children[0].children;assert.equal(cells.length,3);assert.equal(cells[0].textContent,'dev');assert.equal(cells[1].textContent,'x.com');
 const [label,track]=cells[2].children;assert.equal(label.textContent,'13 min left of 15 min');assert.equal(track.getAttribute('role'),'img');
 assert.match(track.getAttribute('aria-label'),/Used: 2 min 26 sec/);assert.equal(track.children[1].style.width,(146/900*100)+'%');
 assert.equal(f.get('overview-date').getAttribute('datetime'),'2026-10-15');
});
test('overview clamps live consumption and preserves blocked/paused/scheduled states',async()=>{
 const f=fixture(),rows=[overviewRow({site:'paused.com',state:'paused'}),overviewRow({site:'blocked.com',state:'always_blocked'}),
 overviewRow({site:'scheduled.com',state:'scheduled_break',remaining_seconds:300}),overviewRow({site:'used.com',used_seconds:1000,remaining_seconds:0,state:'limit_reached'}),overviewRow({site:'negative.com',used_seconds:-5})];
 f.setSnapshot(overviewData(rows));await f.run('refresh()');const budgets=f.get('rows').children.map(tr=>tr.children[2].children);
 assert.equal(budgets[0][1].getAttribute('data-state'),'paused');assert.equal(budgets[0][2].textContent,'Paused');
 assert.equal(budgets[1][1].hidden,true);assert.equal(budgets[1][2].textContent,'Always blocked');
 assert.equal(budgets[2][2].textContent,'Scheduled break');assert.equal(budgets[3][1].children[1].style.width,'100%');assert.equal(budgets[4][1].children[1].style.width,'0%');
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

test('group overview creates members lazily and retains expansion, focus and nodes',async()=>{
 const f=fixture(),data=overviewData([overviewRow({group_id:'fun',budget_mode:'shared',used_seconds:60}),overviewRow({site:'youtube.com',group_id:'fun',budget_mode:'capped',site_budget_seconds:300}),overviewRow({site:'independent.example',group_id:'fun',budget_mode:'independent'})]);
 data.group_overview=[{user:'dev',group_id:'fun',name:'Fun',budget_seconds:1800,remaining_seconds:1740,state:'available'}];
 f.setSnapshot(data);await f.run('refresh()');const details=f.get('rows').querySelectorAll('details')[0];
 assert.equal(details.open,false);assert.equal(details.children[1].children.length,0);assert.equal(f.get('rows').querySelectorAll('progress').length,1);
 details.open=true;details.emit('toggle');assert.equal(details.children[1].children.length,3);
 const member=details.children[1].children[0];assert.match(member.children[2].textContent,/1 min used today/);
 details.children[0].focus();await f.run('refresh()');assert.equal(f.get('rows').querySelectorAll('details')[0],details);assert.equal(details.open,true);assert.equal(f.focus(),details.children[0]);assert.equal(details.children[1].children[0],member);
});
test('blur defaults, validation, draft protection and persistence are independent', async () => {
 const f = fixture([site()]); f.run('openEditor(rules.sites[0])');
 assert.equal(f.get('blur-enabled').checked, false); assert.equal(f.get('blur-fields').hidden, true);
 f.get('blur-enabled').checked = true; f.get('blur-enabled').onchange();
 assert.equal(f.get('blur-minutes').value, '10'); assert.equal(f.get('blur-minutes').disabled, false);
 f.allowDiscard(false); f.get('cancel').onclick(); assert.equal(f.get('site-editor').hidden, false);
 f.get('blur-minutes').value = '15'; await f.submit(); assert.equal(f.requests.length,0); assert.equal(f.focus(),f.get('blur-minutes'));
 f.get('blur-minutes').value = '0'; await f.submit(); assert.equal(f.requests.length,0);
 f.get('blur-minutes').value = '5'; await f.submit();
 assert.equal(f.requests[0].body.blur_enabled,true); assert.equal(f.requests[0].body.blur_after_seconds,300);
 assert.equal(f.requests[0].body.grayscale_enabled,false);
 f.run('openEditor(rules.sites[0])'); assert.equal(f.get('blur-minutes').value,'5'); assert.equal(f.get('blur-enabled').checked,true);
 f.get('always-block').checked = true; f.get('always-block').onchange(); await f.submit(); assert.equal(f.requests[1].body.blur_enabled,false);
});
test('shared blur accepts own threshold beyond group budget and group removal warns', async () => {
 const f = fixture([{...site(),group_id:'fun',budget_mode:'shared',blur_enabled:true,blur_after_seconds:600}]);
 f.run("rules.groups = [{id:'fun',name:'Fun',daily_limit_seconds:60}]; openEditor(rules.sites[0])");
 f.get('blur-minutes').value = '1441'; await f.submit(); assert.equal(f.requests.length,0);
 f.get('blur-minutes').value = '1440'; await f.submit(); assert.equal(f.requests[0].body.blur_after_seconds,86400);
 f.allowDiscard(false); await f.run("removeGroup({id:'fun',name:'Fun',daily_limit_seconds:60})");
 assert.match(f.dialogs.at(-1), /Media blur will be turned off/);
});

const legendText = f => f.get('preview-legend').children.map(li => li.children[1].textContent).join(' | ');
test('Always block controls the whole daily block and preserves drafts and API modes', async () => {
 const f = fixture([site()]); f.run('openEditor(rules.sites[0])');
 assert.equal(f.get('always-block').checked,false); assert.equal(f.focus(), f.get('always-block'));
 f.get('grayscale-enabled').checked=true;f.get('grayscale-enabled').onchange();f.get('grayscale-minutes').value='4';
 f.get('blur-enabled').checked=true;f.get('blur-enabled').onchange();f.get('blur-minutes').value='6';
 f.get('extra-enabled').checked=true;f.get('extra-enabled').onchange();f.get('extra-minutes').value='7';
 f.get('always-block').checked=true;f.get('always-block').onchange();assert.equal(f.get('optional-limits').hidden,true);
 assert.equal(f.get('always-block').getAttribute('aria-expanded'),'false');
 f.allowDiscard(false);f.get('cancel').onclick();assert.equal(f.get('site-editor').hidden,false);
 f.get('always-block').checked=false;f.get('always-block').onchange();
 assert.equal(f.get('grayscale-minutes').value,'4');assert.equal(f.get('blur-minutes').value,'6');assert.equal(f.get('extra-minutes').value,'7');
 assert.equal(f.get('optional-limits').hidden,false);assert.equal(f.get('always-block').getAttribute('aria-expanded'),'true');
 await f.submit();assert.equal(f.requests[0].body.mode,'limit');assert.equal(f.requests[0].body.blur_after_seconds,360);
 f.run('openEditor(rules.sites[0])');f.get('always-block').checked=true;f.get('always-block').onchange();await f.submit();
 assert.equal(f.requests[1].body.mode,'block');assert.equal(f.requests[1].body.grayscale_enabled,false);assert.equal(f.requests[1].body.blur_enabled,false);
 f.run('openEditor(rules.sites[0])');assert.equal(f.get('always-block').checked,true);assert.equal(f.get('optional-limits').hidden,true);
});
for (const [gray, blur, effects] of [
 [null,null,['normal']], [300,null,['normal','grayscale']], [null,300,['normal','blur']],
 [300,600,['normal','grayscale','combined']], [600,300,['normal','blur','combined']], [300,300,['normal','combined']]
]) test(`timeline intervals for grayscale=${gray} blur=${blur}`, () => {
 const f=fixture();const m=f.run(`limitPreviewModel({shared:false,budget:900,gray:${gray},blur:${blur}})`);
 assert.deepEqual(Array.from(m.segments,s=>s.effect),effects);
 assert.equal(m.segments.reduce((sum,s)=>sum+s.end-s.start,0),900);
 assert.equal(m.filters.length,Number(gray!==null)+Number(blur!==null));
 assert.ok(m.segments.every(s=>s.end>s.start));
});
test('live preview shows all manual bonuses with filters continuing and exact seconds', () => {
 const f=fixture();f.get('add-site').onclick();f.get('minutes').value='70';f.get('minutes').emit('input');
 f.get('grayscale-enabled').checked=true;f.get('grayscale-enabled').onchange();f.get('grayscale-minutes').value='30';f.get('grayscale-minutes').emit('input');
 f.get('blur-enabled').checked=true;f.get('blur-enabled').onchange();f.get('blur-minutes').value='40';f.get('blur-minutes').emit('input');
 f.get('extra-enabled').checked=true;f.get('extra-enabled').onchange();f.get('extra-uses').value='3';f.get('extra-uses').emit('input');
 assert.equal(f.get('preview-summary').textContent,'70 min + 3 × 3 min = maximum 79 min');
 assert.match(legendText(f),/30 min–40 min: Grayscale/);assert.match(legendText(f),/70 min–79 min: Grayscale \+ blur \(optional extra time\)/);
 assert.match(legendText(f),/requested manually/);assert.equal(f.requests.length,0);
 const segments=f.get('preview-track').children.filter(n=>n.className.includes('preview-segment'));
 assert.ok(Math.abs(segments.reduce((sum,n)=>sum+parseFloat(n.style.width),0)-100)<0.0001);
 f.get('grayscale-minutes').value=String(1/60);f.get('grayscale-minutes').emit('input');assert.match(legendText(f),/Grayscale starts at 1 sec/);
 f.get('extra-enabled').checked=false;f.get('extra-enabled').onchange();assert.equal(f.get('preview-summary').textContent,'Daily limit: 70 min');
 assert.doesNotMatch(legendText(f),/optional extra time/);
});
test('invalid preview inputs replace the chart without mutating other fields or sending requests', () => {
 const f=fixture();f.get('add-site').onclick();
 for(const invalid of ['', '0','-1','1441','NaN']) {
  f.get('minutes').value=invalid;f.get('minutes').emit('input');assert.equal(f.get('preview-chart').hidden,true);
  assert.match(f.get('preview-message').textContent,/Enter valid/);
 }
 f.get('minutes').value='15';f.get('minutes').emit('input');assert.equal(f.get('preview-chart').hidden,false);
 f.get('blur-enabled').checked=true;f.get('blur-enabled').onchange();
 for(const invalid of ['','15','16']) {f.get('blur-minutes').value=invalid;f.get('blur-minutes').emit('input');assert.equal(f.get('preview-chart').hidden,true)}
 f.get('blur-minutes').value='10';f.get('blur-minutes').emit('input');assert.equal(f.get('preview-chart').hidden,false);
 f.get('extra-enabled').checked=true;f.get('extra-enabled').onchange();
 for(const invalid of ['','0','1.5','101']) {f.get('extra-uses').value=invalid;f.get('extra-uses').emit('input');assert.equal(f.get('preview-chart').hidden,true)}
 f.get('extra-uses').value='2';f.get('extra-uses').emit('input');assert.equal(f.get('preview-chart').hidden,false);
 f.get('extra-minutes').value='0';f.get('extra-minutes').emit('input');assert.equal(f.get('preview-chart').hidden,true);
 assert.equal(f.get('blur-minutes').value,'10');assert.equal(f.requests.length,0);
});
test('shared preview shows own thresholds, read-only group budget and no blocking endpoint or extras', () => {
 const f=fixture([site()]);f.run("rules.groups=[{id:'fun',name:'Fun',daily_limit_seconds:60}];openEditor(rules.sites[0])");
 f.get('site-group').value='fun';f.get('budget-mode').value='shared';f.get('budget-mode').onchange();
 assert.equal(f.get('shared-budget').textContent,'Fun: 1 min shared group budget');assert.equal(f.get('minutes-field').hidden,true);
 assert.equal(f.get('preview-chart').hidden,true);assert.match(f.get('preview-message').textContent,/Enable grayscale or blur/);
 f.get('blur-enabled').checked=true;f.get('blur-enabled').onchange();f.get('blur-minutes').value='20';f.get('blur-minutes').emit('input');
 assert.equal(f.get('preview-chart').hidden,false);assert.match(f.get('preview-end').textContent,/20 min · last filter starts/);
 assert.match(legendText(f),/Blur starts at 20 min/);assert.doesNotMatch(legendText(f),/Daily limit|optional extra time/);
 assert.match(f.get('preview-note').textContent,/not a blocking time/);assert.equal(f.get('extra-enabled').disabled,true);
 f.get('budget-mode').value='capped';f.get('budget-mode').onchange();assert.equal(f.get('preview-chart').hidden,true);
 f.get('blur-minutes').value='10';f.get('blur-minutes').emit('input');assert.equal(f.get('preview-chart').hidden,false);
 assert.match(f.get('preview-note').textContent,/group budget may block access earlier/);
 f.get('budget-mode').value='independent';f.get('budget-mode').onchange();assert.doesNotMatch(f.get('preview-note').textContent,/may block access earlier/);
});

test('continue method defaults, draft protection, preview and saved method survive hidden options',async()=>{
 const f=fixture([site()]);f.run('openEditor(rules.sites[0])');assert.equal(f.get('extra-method').value,'click');assert.equal(f.get('extra-method').disabled,true);
 f.get('extra-enabled').checked=true;f.get('extra-enabled').onchange();f.get('extra-method').value='reflection';f.get('extra-method').onchange();
 assert.match(legendText(f),/reflection and take a 10-second pause for each extension/);
 f.allowDiscard(false);f.get('cancel').onclick();assert.equal(f.get('site-editor').hidden,false);
 f.get('always-block').checked=true;f.get('always-block').onchange();f.get('always-block').checked=false;f.get('always-block').onchange();assert.equal(f.get('extra-method').value,'reflection');
 await f.submit();assert.equal(f.requests[0].body.extra_time_method,'reflection');
 f.run('openEditor(rules.sites[0])');assert.equal(f.get('extra-method').value,'reflection');
 f.get('extra-method').value='math';f.get('extra-method').onchange();assert.match(legendText(f),/Solve a math problem for each extension/);
});

test('site remaining follows selected user and refresh preserves action focus and drafts',async()=>{
 const f=fixture([site()]);const remaining=()=>f.get('sites').children[0].children[4];
 const rows=[overviewRow({user:'Alice',remaining_seconds:120}),overviewRow({user:'Bob',remaining_seconds:420})];
 f.setSnapshot(overviewData(rows));await f.run('refresh()');assert.equal(f.get('sites-user').value,'Alice');assert.equal(remaining().textContent,'2 min');
 f.get('sites-user').value='Bob';f.get('sites-user').onchange();assert.equal(remaining().textContent,'7 min');
 f.run('openEditor(rules.sites[0])');f.get('minutes').value='20';
 const reset=f.get('sites').querySelectorAll('button')[3];reset.focus();
 f.setSnapshot(overviewData(rows.map(r=>({...r,remaining_seconds:r.remaining_seconds-60}))));await f.run('refresh()');
 assert.equal(f.get('sites-user').value,'Bob');assert.equal(remaining().textContent,'6 min');assert.equal(f.get('minutes').value,'20');assert.equal(f.focus(),reset);
});
test('site reset confirms selected user, sends scoped POST and keeps the editor draft',async()=>{
 const f=fixture([site()]);f.setSnapshot(overviewData([overviewRow()]));await f.run('refresh()');
 f.run('openEditor(rules.sites[0])');f.get('minutes').value='20';f.allowDiscard(false);
 await f.get('sites').querySelectorAll('button')[3].onclick();assert.equal(f.requests.filter(r=>r.path.endsWith('/reset')).length,0);
 f.allowDiscard(true);await f.get('sites').querySelectorAll('button')[3].onclick();
 const reset=f.requests.find(r=>r.path.endsWith('/reset'));assert.equal(reset.path,'/api/sites/x.com/reset');assert.equal(reset.method,'POST');assert.deepEqual(reset.body,{user:'dev'});
 assert.match(f.dialogs.at(-1),/x.com \/ dev/);assert.equal(f.get('minutes').value,'20');assert.equal(f.get('site-editor').hidden,false);
});
test('remaining distinguishes blocked and unavailable data, reset prevents duplicate actions',async()=>{
 const f=fixture([site()]);f.setSnapshot(overviewData([overviewRow({state:'always_blocked'})]));await f.run('refresh()');
 assert.equal(f.get('sites').children[0].children[4].textContent,'Always blocked');
 const release=f.hold(),reset=f.get('sites').querySelectorAll('button')[3],saving=reset.onclick();
 assert.equal(reset.disabled,true);assert.equal(f.get('sites-user').disabled,true);await reset.onclick();
 release();await saving;assert.equal(f.requests.filter(r=>r.path.endsWith('/reset')).length,1);
 f.run('stateUnavailable=true;updateSiteRemaining()');assert.equal(f.get('sites').children[0].children[4].textContent,'Unavailable');assert.equal(reset.disabled,true);
});

const timelineRule=(values={})=>({base_budget_seconds:900,grayscale_enabled:false,blur_enabled:false,extra_time_enabled:false,extra_time_seconds:180,...values});
for (const [gray,blur,effects] of [[null,null,['normal']],[300,null,['normal','grayscale']],[null,300,['normal','blur']],[300,600,['normal','grayscale','combined']],[600,300,['normal','blur','combined']],[300,300,['normal','combined']]])
 test(`live timeline colors grayscale=${gray} blur=${blur}`,()=>{
 const f=fixture();const m=f.run(`overviewTimelineModel({budget_mode:'independent'},${JSON.stringify(timelineRule({grayscale_enabled:gray!==null,grayscale_after_seconds:gray,blur_enabled:blur!==null,blur_after_seconds:blur}))})`);
 assert.deepEqual(Array.from(m.segments,s=>s.effect),effects);assert.equal(m.total,900);
 });
test('overview reuses bands for usage changes and recomputes them for grants/settings/day reset',async()=>{
 const f=fixture(),data=overviewData([overviewRow({used_seconds:299})]);data.timeline_rules={'x.com':timelineRule({grayscale_enabled:true,grayscale_after_seconds:300,extra_time_enabled:true})};data.overview[0].extra_time_uses_remaining=2;
 f.setSnapshot(data);await f.run('refresh()');const row=f.get('rows').children[0],track=row.children[2].children[1],bands=track.children[0],first=bands.children[0];
 assert.equal(bands.children.length,3);assert.match(row.children[2].children[3].textContent,/Optional extra up to \+6 min/);
 for(const used of [300,301,900,1000]){data.overview[0].used_seconds=used;f.setSnapshot(data);await f.run('refresh()');assert.equal(f.get('rows').children[0],row);assert.equal(bands.children[0],first);}
 data.overview[0].extra_time_granted_seconds=240;data.overview[0].extra_time_uses_remaining=1;f.setSnapshot(data);await f.run('refresh()');assert.notEqual(bands.children[0],first);assert.match(row.children[2].children[3].textContent,/Extra granted \+4 min/);
 data.timeline_rules['x.com'].blur_enabled=true;data.timeline_rules['x.com'].blur_after_seconds=450;f.setSnapshot(data);await f.run('refresh()');assert.ok(bands.children.some(n=>n.className.includes('combined')));
 data.date='2026-10-16';data.overview[0].used_seconds=0;data.overview[0].extra_time_granted_seconds=0;data.overview[0].extra_time_uses_remaining=2;f.setSnapshot(data);await f.run('refresh()');assert.equal(track.children[1].style.width,'0%');
});
test('shared timelines stop at last threshold and capped text keeps group-effective remaining',()=>{
 const f=fixture();let m=f.run(`overviewTimelineModel({budget_mode:'shared',used_seconds:1000,extra_time_granted_seconds:100,extra_time_uses_remaining:2},${JSON.stringify(timelineRule({blur_enabled:true,blur_after_seconds:300,extra_time_enabled:true}))})`);
 assert.equal(m.total,300);assert.equal(m.usable,300);assert.match(m.summary,/Own time/);assert.ok(m.segments.every(s=>!s.optional));
 m=f.run(`overviewTimelineModel({budget_mode:'shared'},${JSON.stringify(timelineRule())})`);assert.equal(m.total,0);
});
test('simultaneous refresh requests share one in-flight API call',async()=>{
 const f=fixture();f.setSnapshot(overviewData([overviewRow()]));const release=f.hold();const a=f.run('refresh()'),b=f.run('refresh()');assert.equal(f.requests.filter(r=>r.path==='/api/state').length,1);release();await Promise.all([a,b]);
});
test('maximum size overview keeps node count stable across unchanged snapshots',async()=>{
 const f=fixture(),rows=[];for(let user=0;user<64;user++)for(let site=0;site<32;site++)rows.push(overviewRow({user:'user'+user,site:'site'+site+'.com'}));
 f.setSnapshot(overviewData(rows));await f.run('refresh()');const original=[...f.get('rows').children],nodes=()=>{let count=0;const visit=n=>{count++;for(const c of n.children)visit(c)};visit(f.get('rows'));return count};const before=nodes();
 for(let i=0;i<5;i++)await f.run('refresh()');assert.equal(nodes(),before);assert.equal(f.get('rows').children.length,2048);assert.ok(original.every((n,i)=>f.get('rows').children[i]===n));
});

test('closed groups defer updates until reopening and use the latest snapshot',async()=>{
 const f=fixture(),data=overviewData([overviewRow({group_id:'fun',budget_mode:'shared',used_seconds:10})]);data.group_overview=[{user:'dev',group_id:'fun',name:'Fun',budget_seconds:1800,remaining_seconds:1790,state:'available'}];data.timeline_rules={'x.com':timelineRule({blur_enabled:true,blur_after_seconds:300})};
 f.setSnapshot(data);await f.run('refresh()');const details=f.get('rows').querySelectorAll('details')[0];details.open=true;details.emit('toggle');const member=details.children[1].children[0];
 details.open=false;details.emit('toggle');data.overview[0].used_seconds=120;f.setSnapshot(data);await f.run('refresh()');assert.equal(member.children[2].textContent,'<1 min used today');
 details.open=true;details.emit('toggle');assert.equal(member.children[2].textContent,'2 min used today');
 data.overview=[];f.setSnapshot(data);await f.run('refresh()');assert.equal(details.children[1].children.length,0);
});
