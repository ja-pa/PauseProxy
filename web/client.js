(() => {
  if (window.__pauseproxyStarted) return;
  let session, sequence = 0, clicks = 0, pending, busy = false, lastOK = Date.now();
  let stopped = false, heartbeatTimer, healthTimer, beatTimer, lastBeatSent = -Infinity;
 const selection = location.pathname === "/demo" ? location.search : "";
 const base = location.origin + '/__pauseproxy/';
  const fetchRequest = window.fetch.bind(window);
  const root = document.createElement('div');
  root.id = '__pauseproxy-ui';
  const shadow = root.attachShadow({mode: 'closed'});
  // Build DOM without HTML sinks: works with require-trusted-types-for 'script'.
  function element(tag, className, text, parent) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text) node.textContent = text;
    parent.appendChild(node);
    return node;
  }
  const css = element('style', '', '', shadow);
  css.nonce = document.currentScript?.nonce || '';
  css.textContent = /*__PAUSEPROXY_CSS__*/ "";
  const grayscaleCSS = document.createElement('link');
  grayscaleCSS.rel = 'stylesheet'; grayscaleCSS.href = base + 'grayscale.css';
  (document.head || document.documentElement).appendChild(grayscaleCSS);
  function grayscale(on) {
    if (on) document.documentElement.setAttribute('data-pauseproxy-grayscale', '');
    else document.documentElement.removeAttribute('data-pauseproxy-grayscale');
  }
  const blockDocument = document.documentElement.hasAttribute('data-pauseproxy-blocked');
  let extraPending = false, extraRequest, extraSequence = 0;
  const badge = element('div', 'badge', '', shadow);
  const badgeText = element('span', 'badge-text', 'PauseProxy · connecting…', badge);
  function settingsLink(parent) {
    const link = element('a', 'settings-link', '⚙', parent);
    link.setAttribute('aria-label', 'Open settings'); link.title = 'Open settings';
    link.target = '_blank'; link.rel = 'noopener noreferrer'; link.hidden = true;
    return link;
  }
  const badgeSettings = settingsLink(badge);
  const wall = element('div', 'wall', '', shadow);
  const section = element('section', '', '', wall);
  element('p', '', 'PAUSEPROXY', section);
  element('h1', '', 'Time for a break', section);
  const reason = element('p', 'reason', 'Checking the remaining time…', section);
  const wallSettings = settingsLink(section);
  const extra = element('button', '', '', section);
  extra.hidden = true;
  const extraInfo = element('p', '', '', section);
  const extraError = element('p', '', '', section);
  extraError.setAttribute('role', 'status');
  extra.addEventListener('click', async () => {
    if (extraPending || !session || !lastStatus?.extra_time_available) return;
    extraPending = true; extra.disabled = true; extraError.textContent = '';
    extraRequest ||= session.session_id + ':' + (++extraSequence);
    try {
      const result = await post('extra-time', {session_id:session.session_id, instance_id:session.instance_id, request_id:extraRequest});
      extraRequest = null; lastOK = Date.now(); paint(result);
      if (blockDocument && !result.blocked) return;
      activity(); tick();
    } catch (error) { extraError.textContent = error.message; }
    finally { extraPending = false; extra.disabled = false; }
  });
  const reload = element('button', '', 'Reload page', section);
  reload.addEventListener('click', () => location.reload());
  window.__pauseproxyStarted = true;
  console.info('[PauseProxy] Client started; heartbeat every 5 seconds.');
  // A worker installed before PAC can serve an unmodified cached app shell
  // instead of our blocked navigation response. Preventing new workers in the
  // proxy does not remove old registrations. Do this once, for this origin only.
  if (typeof navigator !== 'undefined' && navigator.serviceWorker) {
    navigator.serviceWorker.getRegistrations().then(async registrations => {
      const removed = await Promise.all(registrations.map(registration => registration.unregister()));
      if (removed.some(Boolean)) {
        console.info('[PauseProxy] Old service worker removed. Close other tabs for this site and reopen it to apply proxy rules.');
      }
    }).catch(error => console.warn('[PauseProxy] Could not unregister service worker: ' + error.message));
  }
  let lastError = '';
  let blocked = false;
  const idleTimeout = 60000;
  let lastActivity = performance.now(), idleTimer, lastStatus, unavailable = false, pagePresent = true;
  function active() {
    return pagePresent && document.visibilityState === 'visible' && document.hasFocus() && performance.now() - lastActivity < idleTimeout;
  }
  function armIdleTimer() {
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      if (lastStatus) paint(lastStatus, unavailable);
      tick(); // Stop the server lease when the inactivity threshold is reached.
    }, Math.max(0, idleTimeout - (performance.now() - lastActivity)));
  }
  function activity(event) {
    if (stopped || (event && !event.isTrusted)) return;
    if (!pagePresent || document.visibilityState !== 'visible' || !document.hasFocus()) return;
    const wasActive = active();
    lastActivity = performance.now();
    armIdleTimer();
    if (!wasActive) {
      if (lastStatus) paint(lastStatus, unavailable);
      tick();
    }
  }
  function formatTime(value) {
    const s = Math.max(0, Math.ceil(Number(value) || 0));
    const seconds = String(s % 60).padStart(2, '0');
    return s >= 3600 ? `${Math.floor(s / 3600)}:${String(Math.floor(s / 60) % 60).padStart(2, '0')}:${seconds}` : `${Math.floor(s / 60)}:${seconds}`;
  }
  function paint(status, offline = false) {
    lastStatus = status; unavailable = offline;
    blocked = offline || status.blocked;
    grayscale(!blocked && status.grayscale && !blockDocument);
    extra.hidden = !blocked || offline || !status.extra_time_available;
    extra.disabled = extraPending;
    extra.textContent = `Add ${formatTime(status.extra_time_seconds)}`;
    extraInfo.textContent = !extra.hidden ? `Extensions remaining: ${status.extra_time_uses_remaining}` : '';
    if (blockDocument && !offline && status.tracking !== false && !status.blocked) location.reload();
    wall.style.display = blocked ? 'flex' : 'none';
    reason.textContent = offline ? 'PauseProxy is unavailable. Waiting for a connection.' : status.reason;
    const paused = !blocked && !active() ? ' · paused' : '';
    badgeText.textContent = offline ? 'PauseProxy · offline' : `PauseProxy · ${formatTime(status.remaining_seconds)} left${paused}`;
    badge.className = 'badge' + (blocked ? ' blocked' : status.remaining_seconds <= status.warning_seconds ? ' warning' : '');
    if (blocked) document.querySelectorAll('video,audio').forEach(v => v.pause());
  }
  async function post(path, body) {
    const r = await fetchRequest(base + path + selection, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body), cache:'no-store', keepalive:true, signal:AbortSignal.timeout(4000)});
    let data = await r.json();
    if (!r.ok) { if (r.status === 401) {session = null; pending = null; clicks = 0;} throw new Error(data.error); }
    return data;
  }
  async function tick() {
    if (busy || stopped) return;
    // Focus/pageshow often arrive together. Coalesce active updates so the
    // server's one-second rate limit does not turn normal events into errors.
    // Inactive updates still go immediately to stop the lease on exit.
    clearTimeout(beatTimer);
    const wait = 1100 - (performance.now() - lastBeatSent);
    if (session && active() && wait > 0) {
      beatTimer = setTimeout(tick, wait);
      return;
    }
    busy = true;
    try {
      if (!session) {const created = await post('session', {});
        if (created.admin_url) {
          try { const url = new URL(created.admin_url); if (url.protocol === 'https:' || url.protocol === 'http:') {
            url.hash = 'sites'; for (const link of [badgeSettings, wallSettings]) { link.href = url.href; link.hidden = false; }
          }} catch (_) {}
        }
        const {admin_url, ...credentials} = created; session = credentials; if (session.tracking === false) {stop(); return;} sequence = 0; pending = null; clicks = 0;}
      if (!pending) {pending = {...session, sequence:++sequence, visible:active() && !blocked, click_delta:clicks}; clicks = 0;}
      lastBeatSent = performance.now();
      const result = await post('heartbeat', pending);
      if (result.tracking === false) {stop(); return;}
 if (result.instance_id !== session.instance_id) {session = null; clicks = 0;}
      pending = null; lastOK = Date.now(); lastError = ''; paint(result);
    } catch (error) {
      const message = error.message || 'Connection error';
      if (message !== lastError) { console.warn('[PauseProxy] ' + message); lastError = message; }
      if (Date.now() - lastOK >= 10000) paint({}, true);
    }
    finally {busy = false;}
  }
  document.addEventListener('click', e => {if (e.isTrusted && !e.composedPath().includes(root)) clicks = Math.min(1000, clicks+1);}, true);
  for (const type of ['pointermove','pointerdown','click','keydown','touchstart','wheel','scroll']) {
    document.addEventListener(type, activity, {capture:true, passive:true});
  }
  document.addEventListener('play', e => {if (blocked && e.target.pause) e.target.pause();}, true);
  document.addEventListener('visibilitychange', () => {activity(); tick();});
  window.addEventListener('focus', () => {activity(); tick();});
  window.addEventListener('blur', tick);
  window.addEventListener('pageshow', () => {pagePresent = true; activity(); tick();});
  window.addEventListener('pagehide', () => {pagePresent = false; clearTimeout(idleTimer); if(!stopped && session && !pending) {lastBeatSent=performance.now();fetchRequest(base + 'heartbeat' + selection,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({...session,sequence:++sequence,visible:false,click_delta:clicks}),keepalive:true}).catch(()=>{});clicks=0;}});
  function mount() {(document.body || document.documentElement).appendChild(root);tick();}
  function stop() {grayscale(false);grayscaleCSS.remove();stopped = true;blocked = false;clearTimeout(idleTimer);clearTimeout(beatTimer);clearInterval(heartbeatTimer);clearInterval(healthTimer);root.remove();}
 armIdleTimer(); mount(); heartbeatTimer = setInterval(tick, 5000);
  healthTimer = setInterval(() => {if (Date.now()-lastOK >= 10000) paint({},true); if (!root.isConnected) (document.body || document.documentElement).appendChild(root);}, 1000);
})();
