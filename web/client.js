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
  const blurCSS = document.createElement('link');
  blurCSS.rel = 'stylesheet'; blurCSS.href = base + 'blur.css';
  (document.head || document.documentElement).appendChild(blurCSS);
  function blur(on) {
    if (on) document.documentElement.setAttribute('data-pauseproxy-blur', '');
    else document.documentElement.removeAttribute('data-pauseproxy-blur');
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
  let challengeView = null;
  const challengePanel = element('div', 'continue-challenge', '', section);
  challengePanel.hidden = true;
  element('h2', '', 'Do you want to continue?', challengePanel);
  const mathForm = element('form', 'math-challenge', '', challengePanel);
  const problem = element('p', 'challenge-problem', '', mathForm);
  const answerLabel = element('label', '', 'Your answer', mathForm);
  const answer = element('input', 'challenge-answer', '', answerLabel);
  answer.type = 'number'; answer.inputMode = 'numeric'; answer.step = '1'; answer.autocomplete = 'off'; answer.value = '';
  const check = element('button', 'challenge-check', 'Check answer', mathForm); check.type = 'submit';
  const reflectionPanel = element('div', 'reflection-challenge', '', challengePanel);
  const intentionLabel = element('label', '', 'What exactly do you still want to do on this website?', reflectionPanel);
  const intention = element('textarea', 'challenge-intention', '', intentionLabel);
  intention.rows = 4; intention.maxLength = 500; intention.value = '';
  element('p', 'challenge-note', 'Write at least 3 words and 12 letters, without just repeating words or letters. Your answer is not saved. Maximum 500 characters.', reflectionPanel);
  const countdown = element('p', 'challenge-countdown', '10 s', reflectionPanel);
  countdown.setAttribute('role', 'timer'); countdown.setAttribute('aria-label', 'Pause time remaining');
  const startPause = element('button', 'challenge-start', 'Start pause', reflectionPanel); startPause.type = 'button';
  element('p', 'challenge-note', 'Time only counts while this page is visible. Switching away pauses the countdown.', reflectionPanel);
  const challengeFeedback = element('p', 'challenge-feedback', '', challengePanel);
  challengeFeedback.setAttribute('role', 'status');
  const retryChallenge = element('button', 'challenge-retry', 'Retry challenge', challengePanel); retryChallenge.type = 'button';
  const continueButton = element('button', 'challenge-continue', 'Continue', challengePanel); continueButton.type = 'button';
  const cancelButton = element('button', 'challenge-cancel', 'Cancel', challengePanel); cancelButton.type = 'button';
  function reflectionError(text) {
    const normalized = text.normalize('NFC').toLocaleLowerCase('en');
    const words = normalized.match(/\p{L}[\p{L}\p{M}]*/gu) || [], letters = normalized.match(/\p{L}/gu) || [];
    if ([...text].length > 500) return 'Use at most 500 characters for your reflection.';
    if (words.length && (new Set(words).size === 1 || words.every(w => /^(\p{L})\1*$/u.test(w))))
      return 'Describe your intention without just repeating words or letters.';
    return words.length < 3 || letters.length < 12 ? 'Use at least 3 words and 12 letters to describe your intention.' : '';
  }
  function cancelChallenge(restoreFocus = false) {
    if (challengeView) clearInterval(challengeView.timer);
    challengeView = null; extraRequest = null;
    answer.value = ''; intention.value = ''; answer.removeAttribute('aria-invalid'); intention.removeAttribute('aria-invalid');
    challengeFeedback.textContent = ''; challengePanel.hidden = true;
    if (restoreFocus) { extra.hidden = false; extra.focus(); }
  }
  function updateChallenge() {
    const view = challengeView; if (!view) return;
    const now = performance.now();
    if (view.token && now >= view.expires) { cancelChallenge(true); extraError.textContent = 'Challenge expired. Start a new challenge.'; return; }
    if (view.method === 'reflection') {
      if (view.lastTick != null) view.remaining = Math.max(0, view.remaining - (now - view.lastTick));
      view.lastTick = view.token && document.visibilityState === 'visible' && pagePresent && !unavailable ? now : null;
      countdown.textContent = Math.ceil(view.remaining / 1000) + ' s';
      const error = reflectionError(intention.value);
      intention.setAttribute('aria-invalid', String(!!error && intention.value.trim().length > 0));
      if (!view.loading) challengeFeedback.textContent = view.error || (error && intention.value.trim() ? error
        : !view.token ? 'Write your intention and start the 10-second pause.'
        : view.remaining > 0 ? 'Take a moment to consider whether you want to continue.'
        : error || 'Your reflection and pause are complete. Do you want to continue?');
      continueButton.disabled = extraPending || unavailable || !!error || !view.token || view.remaining > 0;
      startPause.disabled = extraPending || unavailable || view.loading || !!view.token;
    } else {
      continueButton.disabled = extraPending || unavailable || !view.mathReady;
      answer.disabled = check.disabled = view.loading || !view.token || view.mathReady || extraPending || unavailable;
    }
    retryChallenge.hidden = view.method !== 'math' || !!view.token || view.loading;
    retryChallenge.disabled = unavailable || view.loading;
    cancelButton.disabled = extraPending;
  }
  async function loadChallenge() {
    const view = challengeView; if (!view || view.loading || !session) return;
    view.loading = true; view.error = ''; challengeFeedback.textContent = 'Preparing your pause…'; updateChallenge();
    extraRequest ||= session.session_id + ':' + (++extraSequence);
    try {
      const result = await post('extra-time/challenge', {session_id:session.session_id, instance_id:session.instance_id, request_id:extraRequest});
      if (view !== challengeView) return;
      if (result.method !== view.method) { cancelChallenge(true); extraError.textContent = 'Continue method changed. Start again.'; return; }
      view.token = result.challenge_id; view.expires = performance.now() + 300000;
      if (view.method === 'math') {view.a = result.a; view.b = result.b; problem.textContent = `${view.a} × ${view.b} = ?`;}
      else view.lastTick = document.visibilityState === 'visible' && !unavailable ? performance.now() : null;
      lastOK = Date.now();
    } catch (error) {
      if (view !== challengeView) return;
      challengeFeedback.textContent = error.message; view.error = error.message;
    } finally {
      if (view === challengeView) {
        view.loading = false; updateChallenge();
        if (view.error) challengeFeedback.textContent = view.error;
        if (view.method === 'math' && view.token) answer.focus();
      }
    }
  }
  function openChallenge(method) {
    cancelChallenge();
    challengeView = {method, uses:lastStatus.extra_time_uses_remaining, remaining:10000, lastTick:null, token:null, mathReady:false};
    const view = challengeView;
    extraError.textContent = ''; challengePanel.hidden = false; extra.hidden = true;
    mathForm.hidden = method !== 'math'; reflectionPanel.hidden = method !== 'reflection';
    answer.value = ''; intention.value = ''; problem.textContent = ''; countdown.textContent = '10 s';
    view.timer = setInterval(updateChallenge, 100); updateChallenge();
    if (method === 'math') loadChallenge(); else intention.focus();
  }
  mathForm.addEventListener('submit', event => {
    event.preventDefault(); const view = challengeView;
    if (!view || !view.token || unavailable || extraPending) return;
    view.mathReady = answer.value.trim() !== '' && Number(answer.value) === view.a * view.b;
    answer.setAttribute('aria-invalid', String(!view.mathReady)); updateChallenge();
    challengeFeedback.textContent = view.mathReady ? 'Correct. Now you can decide whether to continue.' : 'That is not the correct answer. Try the same problem again.';
    (view.mathReady ? continueButton : answer).focus();
  });
  intention.addEventListener('input', () => {if (challengeView) challengeView.error = ''; updateChallenge();});
  startPause.addEventListener('click', loadChallenge);
  retryChallenge.addEventListener('click', loadChallenge);
  cancelButton.addEventListener('click', () => { if (!extraPending) cancelChallenge(true); });
  async function grantExtra() {
    if (extraPending || !session || !lastStatus?.extra_time_available || unavailable) return;
    const view = challengeView;
    if (view) {updateChallenge(); if (continueButton.disabled || view !== challengeView) return;}
    extraPending = true; extra.disabled = true; extraError.textContent = ''; updateChallenge();
    extraRequest ||= session.session_id + ':' + (++extraSequence);
    try {
      const result = await post('extra-time', {session_id:session.session_id, instance_id:session.instance_id, request_id:extraRequest,
        ...(view ? {challenge_id:view.token, ...(view.method === 'math' ? {answer:Number(answer.value)} : {reflection:intention.value})} : {})});
      cancelChallenge(); lastOK = Date.now(); paint(result);
      if (blockDocument && !result.blocked) return;
      activity(); tick();
    } catch (error) {
      extraError.textContent = error.message;
      if (error.status === 409 && view) cancelChallenge(true);
    } finally {extraPending = false; extra.disabled = false; updateChallenge();}
  }
  continueButton.addEventListener('click', grantExtra);
  extra.addEventListener('click', () => {
    if (extraPending || !session || !lastStatus?.extra_time_available || unavailable) return;
    const method = lastStatus.extra_time_method || 'click';
    if (method === 'click') return grantExtra();
    openChallenge(method);
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
    blur(!blocked && status.blur && !blockDocument);
    if (!offline && challengeView && (!session || !blocked || !status.extra_time_available || (status.extra_time_method || 'click') !== challengeView.method || status.extra_time_uses_remaining !== challengeView.uses)) cancelChallenge();
    challengePanel.hidden = !challengeView || offline;
    extra.hidden = !!challengeView || !blocked || offline || !status.extra_time_available;
    extra.disabled = extraPending;
    extra.textContent = (status.extra_time_method === 'math' ? 'Solve math to add ' : status.extra_time_method === 'reflection' ? 'Reflect to add ' : 'Add ') + formatTime(status.extra_time_seconds);
    extraInfo.textContent = blocked && !offline && status.extra_time_available ? `Extensions remaining: ${status.extra_time_uses_remaining}` : '';
    if (blockDocument && !offline && status.tracking !== false && !status.blocked) location.reload();
    wall.style.display = blocked ? 'flex' : 'none';
    reason.textContent = offline ? 'PauseProxy is unavailable. Waiting for a connection.' : status.reason;
    const paused = !blocked && !active() ? ' · paused' : '';
    badgeText.textContent = offline ? 'PauseProxy · offline' : `PauseProxy · ${formatTime(status.remaining_seconds)} left${paused}`;
    badge.className = 'badge' + (blocked ? ' blocked' : status.remaining_seconds <= status.warning_seconds ? ' warning' : '');
    if (blocked) document.querySelectorAll('video,audio').forEach(v => v.pause());
    updateChallenge();
  }
  async function post(path, body) {
    const r = await fetchRequest(base + path + selection, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body), cache:'no-store', keepalive:true, signal:AbortSignal.timeout(4000)});
    let data = await r.json();
    if (!r.ok) { if (r.status === 401) {session = null; pending = null; clicks = 0;} const error = new Error(data.error); error.status = r.status; if (r.status === 401) cancelChallenge(); throw error; }
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
      if (!session) {cancelChallenge(); const created = await post('session', {});
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
 if (result.instance_id !== session.instance_id) {cancelChallenge();session = null; clicks = 0;}
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
  document.addEventListener('visibilitychange', () => {updateChallenge();activity(); tick();});
  window.addEventListener('focus', () => {activity(); tick();});
  window.addEventListener('blur', tick);
  window.addEventListener('pageshow', () => {pagePresent = true; activity(); tick();});
  window.addEventListener('pagehide', () => {pagePresent = false; cancelChallenge(); clearTimeout(idleTimer); if(!stopped && session && !pending) {lastBeatSent=performance.now();fetchRequest(base + 'heartbeat' + selection,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({...session,sequence:++sequence,visible:false,click_delta:clicks}),keepalive:true}).catch(()=>{});clicks=0;}});
  function mount() {(document.body || document.documentElement).appendChild(root);tick();}
  function stop() {cancelChallenge();grayscale(false);grayscaleCSS.remove();blur(false);blurCSS.remove();stopped = true;blocked = false;clearTimeout(idleTimer);clearTimeout(beatTimer);clearInterval(heartbeatTimer);clearInterval(healthTimer);root.remove();}
 armIdleTimer(); mount(); heartbeatTimer = setInterval(tick, 5000);
  healthTimer = setInterval(() => {if (Date.now()-lastOK >= 10000) paint({},true); if (!root.isConnected) (document.body || document.documentElement).appendChild(root);}, 1000);
})();
