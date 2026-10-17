const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');

// Run the complete embedded client with a controlled clock and browser events.
function fixture(options = {}) {
  let focusNode, challengeNumber = 0; const challenges = new Map();
  let now = 0, nextTimer = 0, focused = true, offline = false, reloads = 0;
  const timers = new Map(), requests = [], nodes = [], events = new Map();
  const eventTarget = name => ({
    addEventListener(type, fn) {
      const key = name + type;
      if (!events.has(key)) events.set(key, []);
      events.get(key).push(fn);
    }
  });
  function node(tag) {
    const n = {...eventTarget(tag), tag, style: {}, isConnected: false, textContent: '',
      setAttribute(key,value) {this[key]=value;},
      removeAttribute(key) {delete this[key];}, hasAttribute(key) {return Object.hasOwn(this,key);},
      remove() {this.isConnected=false;},
      appendChild(child) {child.isConnected = true; return child;},
      focus() {focusNode = this;},
      attachShadow() {return node('shadow');}
    };
    n.listeners = new Map(); const register = n.addEventListener; n.addEventListener = (type, fn) => {n.listeners.set(type, fn); register(type, fn);};
    nodes.push(n); return n;
  }
  const document = {...eventTarget('document'), visibilityState: 'visible',
    hasFocus: () => focused, createElement: node, body: node('body'),
    documentElement: node('html'), querySelectorAll: () => []};
  if(options.blockDocument) document.documentElement.setAttribute('data-pauseproxy-blocked','');
  const status = {instance_id: 'instance', remaining_seconds: 100, warning_seconds: 30, blocked: !!options.blockDocument, extra_time_method:options.method || 'click'};
  const window = {crypto:{randomUUID:()=> 'request-1'},...eventTarget('window'), fetch: async (url, requestOptions) => {
    if (offline) throw Error("offline");
    const body = JSON.parse(requestOptions.body); requests.push({url, body});
    if(url.includes('/extra-time/challenge')) {
      if (options.challengeFailure?.()) throw Error('Lost challenge response');
      if (!challenges.has(body.request_id)) challenges.set(body.request_id, {challenge_id:'challenge-'+(++challengeNumber),method:status.extra_time_method,a:2+challengeNumber,b:4});
      return {ok:true,status:200,json:async()=>challenges.get(body.request_id)};
    }
    if(url.endsWith('/extra-time')) { if(options.extraFailure?.()) throw Error('Lost response'); status.blocked=false;status.grayscale=true;status.extra_time_available=false;status.remaining_seconds=180; }
    return {ok: true, status: 200, json: async () => url.endsWith('/session')
      ? {session_id: 'session', instance_id: 'instance', admin_url:options.adminURL || 'https://192.168.1.50:9083'} : status};
  }};
  function timer(fn, delay, interval = false) {
    const id = ++nextTimer; timers.set(id, {fn, at: now + delay, delay, interval}); return id;
  }
  vm.runInNewContext(readFileSync(__dirname + '/client.js', 'utf8'), {
    URL, window, document, navigator: options.navigator || {}, location: {origin: 'https://x.com', reload() {reloads++;}},
    performance: {now: () => now}, Date: {now: () => now},
    console: {info() {}, warn() {}}, AbortSignal: {timeout() {}},
    setTimeout: (fn, delay) => timer(fn, delay), clearTimeout: id => timers.delete(id), clearInterval: id => timers.delete(id),
    setInterval: (fn, delay) => timer(fn, delay, true)
  });
  async function flush() {for (let i = 0; i < 20; i++) await Promise.resolve();}
  return {
    requests, status, nodes, reloads:()=>reloads, focusNode:()=>focusNode,
    control: name => nodes.find(n => n.className === name),
    offline: value => {offline = value;},
    lastBeat: () => requests.filter(r => r.url.endsWith('/heartbeat')).at(-1).body,
    badge: () => nodes.find(n => n.className === 'badge-text').textContent,
    focus: value => {focused = value;}, document,
    async emit(target, type, trusted = true) {
      for (const fn of events.get(target + type) || []) fn({isTrusted: trusted, composedPath: () => []});
      await flush();
    },
    async advance(ms) {
      await flush(); const end = now + ms;
      while (true) {
        const due = [...timers].filter(([,t]) => t.at <= end).sort((a,b) => a[1].at - b[1].at)[0];
        if (!due) break;
        const [id, t] = due; now = t.at;
        if (t.interval) t.at += t.delay; else timers.delete(id);
        t.fn(); await flush();
      }
      now = end; await flush();
    }
  };
}

test('idle after one minute; trusted activity resumes without a new session', async () => {
  const f = fixture(); await f.advance(0);
  assert.equal(f.lastBeat().visible, true);
  await f.advance(59000); assert.equal(f.lastBeat().visible, true);
  await f.advance(1000); assert.equal(f.lastBeat().visible, false);
  assert.match(f.badge(), /paused/);
  await f.emit('document', 'pointermove', false);
  await f.advance(5000); assert.equal(f.lastBeat().visible, false);
  await f.emit('document', 'pointermove'); await f.advance(1100); assert.equal(f.lastBeat().visible, true);
  assert.doesNotMatch(f.badge(), /paused/);
  assert.equal(f.requests.filter(r => r.url.endsWith('/session')).length, 1);
});

test('focus, visibility and page exit stop measurement, return resumes it', async () => {
  const f = fixture(); await f.advance(2000);
  f.focus(false); await f.emit('window', 'blur'); assert.equal(f.lastBeat().visible, false);
  await f.advance(5000); assert.equal(f.lastBeat().visible, false);
  f.focus(true); await f.emit('window', 'focus'); assert.equal(f.lastBeat().visible, true);
  f.document.visibilityState = 'hidden'; await f.emit('document', 'visibilitychange');
  assert.equal(f.lastBeat().visible, false);
  f.document.visibilityState = 'visible'; await f.emit('document', 'visibilitychange');
  await f.advance(1100);
  assert.equal(f.lastBeat().visible, true);
  await f.emit('window', 'pagehide'); assert.equal(f.lastBeat().visible, false);
  await f.advance(5000); assert.equal(f.lastBeat().visible, false);
  await f.emit('window', 'pageshow'); await f.advance(1100); assert.equal(f.lastBeat().visible, true);
});

test('focus and pageshow bursts coalesce active beats, exit still stops immediately', async () => {
  const f = fixture(); await f.advance(0);
  const beats = () => f.requests.filter(r => r.url.endsWith('/heartbeat'));
  await f.emit('window', 'focus'); await f.emit('window', 'pageshow');
  await f.emit('document', 'visibilitychange');
  assert.equal(beats().length, 1);
  await f.advance(1099); assert.equal(beats().length, 1);
  await f.advance(1); assert.equal(beats().length, 2);
  f.focus(false); await f.emit('window', 'blur');
  assert.equal(beats().length, 3); assert.equal(f.lastBeat().visible, false);
  f.focus(true); await f.emit('window', 'focus');
  assert.equal(beats().length, 3);
  await f.advance(1100); assert.equal(beats().length, 4); assert.equal(f.lastBeat().visible, true);
});

test('keyboard, touch and scrolling reset inactivity; clicks keep their count', async () => {
  const f = fixture(); await f.advance(30000);
  await f.emit('document', 'keydown'); await f.advance(30000);
  assert.equal(f.lastBeat().visible, true);
  await f.emit('document', 'touchstart'); await f.advance(30000);
  await f.emit('document', 'scroll'); await f.advance(30000);
  assert.equal(f.lastBeat().visible, true);
  await f.emit('document', 'click'); await f.advance(5000);
  assert.equal(f.lastBeat().click_delta, 1);
  f.status.blocked = true; await f.advance(5000); await f.advance(5000);
  assert.equal(f.lastBeat().visible, false);
  f.status.blocked = false; await f.advance(10000);
  assert.equal(f.lastBeat().visible, true);
});

test('removed rule stops polling and removes the blocking UI', async () => {
 const f=fixture(); await f.advance(0); f.status.tracking=false; await f.advance(5000);
 const count=f.requests.length; await f.advance(60000); await f.emit('document','pointermove');
 assert.equal(f.requests.length,count);
});

test('old origin workers are unregistered once without stopping heartbeat', async () => {
  let lists = 0, removed = 0;
  const f = fixture({navigator:{serviceWorker:{getRegistrations:async()=>{
    lists++;
    return [{unregister:async()=>{removed++;return true;}},{unregister:async()=>{removed++;return false;}}];
  }}}});
  await f.advance(15000);
  assert.equal(lists,1); assert.equal(removed,2);
  assert.equal(f.lastBeat().visible,true);
});

test('worker cleanup failure does not stop the blocking overlay', async () => {
  const f = fixture({navigator:{serviceWorker:{getRegistrations:async()=>{throw new Error('denied');}}}});
  await f.advance(0);f.status.blocked=true;await f.advance(10000);
  assert.equal(f.lastBeat().visible,false);
});


test('remaining time boundaries and settings links survive repaint and blocking', async () => {
  const f = fixture(); await f.advance(0);
  for (const [seconds, expected] of [[0,'0:00'],[59,'0:59'],[60,'1:00'],[3599,'59:59'],[3600,'1:00:00'],[59.1,'1:00'],[-3,'0:00']]) {
    f.status.remaining_seconds = seconds; await f.advance(5000);
    assert.ok(f.badge().includes(expected + ' left'), f.badge());
  }
  const links = f.nodes.filter(n => n.className === 'settings-link');
  assert.equal(links.length, 2);
  f.status.blocked=true; await f.advance(5000);
  for (const link of links) {
    assert.equal(link.href, 'https://192.168.1.50:9083/#sites');
    assert.equal(link.hidden, false); assert.equal(link.target,'_blank');
    assert.equal(link['aria-label'], 'Open settings');
  }
  assert.equal(f.lastBeat().admin_url, undefined, 'display metadata must not be sent in strict heartbeat JSON');
});


test('offline overlay keeps settings accessible and recovers on a heartbeat', async () => {
  const f=fixture(); await f.advance(0); f.offline(true); await f.advance(11000);
  assert.match(f.badge(), /offline/);
  assert.equal(f.nodes.find(n=>n.className==='wall').style.display,'flex');
  assert.ok(f.nodes.filter(n=>n.className==='settings-link').every(n=>!n.hidden && n.href.endsWith('/#sites')));
  f.offline(false); await f.advance(5000);
  assert.doesNotMatch(f.badge(), /offline/);
  assert.equal(f.nodes.find(n=>n.className==='wall').style.display,'none');
});

 test('HTTP settings links work from proxied HTTPS pages', async () => {
  const f = fixture({adminURL:'http://192.168.1.50:9083'}); await f.advance(0);
  const links = f.nodes.filter(n => n.className === 'settings-link');
  assert.equal(links.length, 2);
  for (const link of links) assert.equal(link.href, 'http://192.168.1.50:9083/#sites');
});

test('grayscale follows server state and clears on block, offline and removal',async()=>{
 const f=fixture();await f.advance(0);f.status.grayscale=true;await f.advance(5000);
 assert.ok(f.document.documentElement.hasAttribute('data-pauseproxy-grayscale'));
 f.status.blocked=true;f.status.extra_time_available=true;f.status.extra_time_seconds=180;f.status.extra_time_uses_remaining=2;await f.advance(5000);
 assert.ok(!f.document.documentElement.hasAttribute('data-pauseproxy-grayscale'));
 assert.ok(f.nodes.some(n=>n.tag==='button'&&!n.hidden&&n.textContent==='Add 3:00'));
 f.status.blocked=false;await f.advance(5000);assert.ok(f.document.documentElement.hasAttribute('data-pauseproxy-grayscale'));
 f.offline(true);await f.advance(15000);assert.ok(!f.document.documentElement.hasAttribute('data-pauseproxy-grayscale'));
 f.offline(false);f.status.tracking=false;await f.advance(5000);assert.ok(!f.document.documentElement.hasAttribute('data-pauseproxy-grayscale'));
});

test('bonus retry keeps request ID, resumes activity and remains grayscale',async()=>{
 let fail=true;const f=fixture({extraFailure:()=>fail});await f.advance(0);
 Object.assign(f.status,{blocked:true,extra_time_available:true,extra_time_seconds:180,extra_time_uses_remaining:1});await f.advance(5000);
 const button=f.nodes.find(n=>n.tag==='button'&&n.textContent==='Add 3:00');
 await button.listeners.get('click')();assert.equal(button.disabled,false);assert.ok(f.nodes.some(n=>n.textContent==='Lost response'));
 fail=false;await button.listeners.get('click')();await f.advance(1100);
 const grants=f.requests.filter(r=>r.url.endsWith('/extra-time'));assert.equal(grants.length,2);assert.equal(grants[0].body.request_id,grants[1].body.request_id);
 assert.ok(f.document.documentElement.hasAttribute('data-pauseproxy-grayscale'));assert.equal(f.lastBeat().visible,true);assert.equal(f.reloads(),0);
});
test('server block document reloads once after bonus',async()=>{
 const f=fixture({blockDocument:true});await f.advance(0);Object.assign(f.status,{extra_time_available:true,extra_time_seconds:180,extra_time_uses_remaining:1});await f.advance(5000);
 const button=f.nodes.find(n=>n.tag==='button'&&n.textContent==='Add 3:00');await button.listeners.get('click')();assert.equal(f.reloads(),1);
 assert.ok(!f.document.documentElement.hasAttribute('data-pauseproxy-grayscale'));
});

test('expired budget after reload keeps the break screen and never reloads automatically', async()=>{
 const f=fixture({blockDocument:true});await f.advance(15000);
 assert.equal(f.nodes.find(n=>n.className==='wall').style.display,'flex');
 assert.equal(f.reloads(),0);
 f.offline(true);await f.advance(15000);
 assert.equal(f.nodes.find(n=>n.className==='wall').style.display,'flex');
 f.offline(false);await f.advance(5000);
 assert.equal(f.nodes.find(n=>n.className==='wall').style.display,'flex');
 assert.equal(f.reloads(),0);
 assert.ok(f.nodes.some(n=>n.tag==='style'));
 assert.ok(!f.nodes.some(n=>n.href?.endsWith('/client.css')));
});

for (const gray of [false, true]) for (const blur of [false, true]) {
 test(`independent filters grayscale=${gray} blur=${blur}`, async () => {
  const f = fixture(); f.status.grayscale = gray; f.status.blur = blur; await f.advance(0);
  assert.equal(f.document.documentElement.hasAttribute('data-pauseproxy-grayscale'), gray);
  assert.equal(f.document.documentElement.hasAttribute('data-pauseproxy-blur'), blur);
 });
}
test('blur clears on block, offline, removal and restores after bonus', async () => {
 const f = fixture(); f.status.blur = true; await f.advance(0);
 const on = () => f.document.documentElement.hasAttribute('data-pauseproxy-blur');
 assert.ok(on()); f.status.blocked = true; f.status.extra_time_available = true; await f.advance(5000); assert.ok(!on());
 const extra = f.nodes.find(n => n.tag === 'button' && n.listeners.has('click'));
 await extra.listeners.get('click')(); assert.ok(on());
 f.offline(true); await f.advance(15000); assert.ok(!on());
 f.offline(false); await f.advance(5000); assert.ok(on());
 f.status.tracking = false; await f.advance(5000); assert.ok(!on());
 assert.equal(f.nodes.find(n => n.href?.endsWith('/blur.css')).isConnected, false);
});
test('blocking document never blurs even if server flag is set', async () => {
 const f = fixture({blockDocument:true}); f.status.blur = true; await f.advance(0);
 assert.ok(!f.document.documentElement.hasAttribute('data-pauseproxy-blur'));
});

async function challengeFixture(method, options={}) {
 const f=fixture({...options,method});Object.assign(f.status,{blocked:true,extra_time_available:true,extra_time_seconds:180,extra_time_uses_remaining:3});await f.advance(0);
 await f.nodes.find(n=>n.tag==='button').listeners.get('click')();await f.advance(0);return f;
}
async function press(f,name,event='click') {await f.control(name).listeners.get(event)({preventDefault(){}});await f.advance(0);}
test('math unlocks Continue only for correct answer; preserves task on heartbeats and requires new task next time',async()=>{
 const f=await challengeFixture('math');const problem=f.control('challenge-problem').textContent;
 assert.equal(f.control('continue-challenge').hidden,false);assert.equal(f.control('challenge-continue').disabled,true);
 f.control('challenge-answer').value='999';await press(f,'math-challenge','submit');assert.equal(f.control('challenge-continue').disabled,true);
 assert.equal(f.requests.filter(r=>r.url.endsWith('/extra-time')).length,0);
 await f.advance(5000);assert.equal(f.control('challenge-problem').textContent,problem);
 const [a,b]=problem.match(/\d+/g).map(Number);f.control('challenge-answer').value=String(a*b);await press(f,'math-challenge','submit');
 assert.equal(f.control('challenge-continue').disabled,false);assert.equal(f.focusNode(),f.control('challenge-continue'));
 await press(f,'challenge-continue');const grant=f.requests.find(r=>r.url.endsWith('/extra-time'));assert.equal(grant.body.answer,a*b);assert.ok(grant.body.challenge_id);
 assert.equal(f.control('continue-challenge').hidden,true);assert.equal(f.control('challenge-answer').value,'');
 Object.assign(f.status,{blocked:true,extra_time_available:true,extra_time_uses_remaining:2});await f.advance(5000);
 await f.nodes.find(n=>n.tag==='button').listeners.get('click')();await f.advance(0);
 assert.notEqual(f.control('challenge-problem').textContent,problem);assert.equal(f.control('challenge-continue').disabled,true);
});
test('reflection needs valid text and ten visible seconds, clears text on success',async()=>{
 const f=await challengeFixture('reflection');assert.equal(f.requests.filter(r=>r.url.includes('/challenge')).length,0);
 f.control('challenge-intention').value='aaaa bbbb cccc';await press(f,'challenge-intention','input');
 await press(f,'challenge-start');await f.advance(5000);assert.equal(f.control('challenge-countdown').textContent,'5 s');
 f.document.visibilityState='hidden';await f.emit('document','visibilitychange');await f.advance(20000);assert.equal(f.control('challenge-countdown').textContent,'5 s');
 f.document.visibilityState='visible';await f.emit('document','visibilitychange');await f.advance(5000);assert.equal(f.control('challenge-countdown').textContent,'0 s');
 assert.equal(f.control('challenge-continue').disabled,true);
 f.control('challenge-intention').value='Chci odpovědět na zprávu';await press(f,'challenge-intention','input');assert.equal(f.control('challenge-continue').disabled,false);
 await press(f,'challenge-continue');assert.equal(f.control('challenge-intention').value,'');
 assert.equal(f.requests.find(r=>r.url.endsWith('/extra-time')).body.reflection,'Chci odpovědět na zprávu');
});
test('cancel discards reflection and timer without granting; method change and page exit reset tasks',async()=>{
 const f=await challengeFixture('reflection');f.control('challenge-intention').value='Chci odpovědět na zprávu';await press(f,'challenge-start');await f.advance(1000);await press(f,'challenge-cancel');
 assert.equal(f.control('challenge-intention').value,'');assert.equal(f.control('continue-challenge').hidden,true);assert.equal(f.requests.filter(r=>r.url.endsWith('/extra-time')).length,0);
 f.status.extra_time_method='math';await f.advance(5000);await f.nodes.find(n=>n.tag==='button').listeners.get('click')();await f.advance(0);assert.equal(f.control('continue-challenge').hidden,false);
 f.status.extra_time_method='reflection';await f.advance(5000);assert.equal(f.control('continue-challenge').hidden,true);
 await f.nodes.find(n=>n.tag==='button').listeners.get('click')();await f.advance(0);await f.emit('window','pagehide');assert.equal(f.control('continue-challenge').hidden,true);
});
test('challenge creation failures can retry, bonus failures keep same request and answer',async()=>{
 let failures=1;const f=await challengeFixture('math',{challengeFailure:()=>failures-->0});
 assert.equal(f.control('challenge-retry').hidden,false);await press(f,'challenge-retry');assert.equal(f.control('challenge-retry').hidden,true);
 const p=f.control('challenge-problem').textContent;const [a,b]=p.match(/\d+/g).map(Number);
 f.control('challenge-answer').value=String(a*b);await press(f,'math-challenge','submit');
 await f.advance(5000);assert.equal(f.control('challenge-answer').value,String(a*b));
 await press(f,'challenge-cancel');assert.equal(f.control('challenge-answer').value,'');
 let lost=1;const g=await challengeFixture('math',{extraFailure:()=>lost-->0});const [x,y]=g.control('challenge-problem').textContent.match(/\d+/g).map(Number);
 g.control('challenge-answer').value=String(x*y);await press(g,'math-challenge','submit');await press(g,'challenge-continue');assert.equal(g.control('continue-challenge').hidden,false);
 await press(g,'challenge-continue');const grants=g.requests.filter(r=>r.url.endsWith('/extra-time'));assert.equal(grants.length,2);assert.deepEqual(grants[0].body,grants[1].body);
});
test('reflection network failure retries Start pause without losing intention',async()=>{
 let lost=1;const f=await challengeFixture('reflection',{challengeFailure:()=>lost-->0});f.control('challenge-intention').value='Chci odpovědět na zprávu';await press(f,'challenge-start');
 assert.equal(f.control('challenge-start').disabled,false);assert.equal(f.control('challenge-intention').value,'Chci odpovědět na zprávu');assert.match(f.control('challenge-feedback').textContent,/Lost challenge/);
 await press(f,'challenge-start');await f.advance(10000);assert.equal(f.control('challenge-continue').disabled,false);
});
test('challenge flow on a blocking document reloads only after completed grant',async()=>{
 const f=await challengeFixture('math',{blockDocument:true});assert.equal(f.reloads(),0);
 const [a,b]=f.control('challenge-problem').textContent.match(/\d+/g).map(Number);f.control('challenge-answer').value=String(a*b);await press(f,'math-challenge','submit');assert.equal(f.reloads(),0);
 await press(f,'challenge-continue');assert.equal(f.reloads(),1);
});

test('eligibility loss and a new server instance invalidate the open task',async()=>{
 for (const change of ['availability','instance','tracking']) {
  const f=await challengeFixture('reflection');f.control('challenge-intention').value='Chci odpovědět na zprávu';
  if(change==='availability') f.status.extra_time_available=false;
  if(change==='instance') f.status.instance_id='new-instance';
  if(change==='tracking') f.status.tracking=false;
  await f.advance(5000);assert.equal(f.control('continue-challenge').hidden,true);assert.equal(f.control('challenge-intention').value,'');
 }
});
test('five-minute client challenge expiry clears proof and allows a new task',async()=>{
 const f=await challengeFixture('math');await f.advance(300000);assert.equal(f.control('continue-challenge').hidden,true);
 assert.equal(f.control('challenge-answer').value,'');assert.equal(f.requests.filter(r=>r.url.endsWith('/extra-time')).length,0);
});
