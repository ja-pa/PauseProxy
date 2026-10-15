const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');

// Run the complete embedded client with a controlled clock and browser events.
function fixture(options = {}) {
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
      attachShadow() {return node('shadow');}
    };
    n.listeners = new Map(); const register = n.addEventListener; n.addEventListener = (type, fn) => {n.listeners.set(type, fn); register(type, fn);};
    nodes.push(n); return n;
  }
  const document = {...eventTarget('document'), visibilityState: 'visible',
    hasFocus: () => focused, createElement: node, body: node('body'),
    documentElement: node('html'), querySelectorAll: () => []};
  if(options.blockDocument) document.documentElement.setAttribute('data-pauseproxy-blocked','');
  const status = {instance_id: 'instance', remaining_seconds: 100, warning_seconds: 30, blocked: !!options.blockDocument};
  const window = {crypto:{randomUUID:()=> 'request-1'},...eventTarget('window'), fetch: async (url, requestOptions) => {
    if (offline) throw Error("offline");
    const body = JSON.parse(requestOptions.body); requests.push({url, body});
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
    requests, status, nodes, reloads:()=>reloads,
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
