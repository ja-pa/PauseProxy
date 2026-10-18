# PauseProxy — developer guide

This document describes the current implementation, its invariants, testing and known issues. For startup and browser setup, see [README.md](README.md) and the [howto page](http://127.0.0.1:9083/howto).

## Local development

For the first run, copy `config.dev.json` to the ignored `config.local.json` and set a password in the copy.

Requires Go ≥ 1.26. Development scripts can install pinned Go 1.27.1. Windows and Linux use separate toolchains; they can share the CA and rules.

### Ubuntu WSL

From the project root:

```sh
bash ./run_test.sh --check       # go test, go vet, go test -race
bash ./run_test.sh               # checks, build, CA bootstrap if needed, start
node --test web/client_test.cjs web/admin_test.cjs  # separate; run_test.sh does not run JavaScript tests
```

The race detector needs GCC (`build-essential`). `--skip-race` explicitly omits a check and is not full validation. `--check` neither starts the server nor creates a CA. The script can also be called from another working directory.

IPK builds, procd and router tests live in [PauseProxy-wrt](https://github.com/ja-pa/PauseProxy-wrt). Make application changes here, commit them and update the pinned submodule in the WRT repository.

To use a custom configuration after preparing the CA:

```sh
.tools/go-linux/go/bin/go build -o dist/pauseproxy-linux ./cmd/pauseproxy
./dist/pauseproxy-linux --config ./config.android.json
```

If the script used system Go without downloading a local toolchain, replace the path with `go`. Without `--check`, the script prefers the ignored `config.local.json`, falling back to the `config.dev.json` template.

### Windows PowerShell

```powershell
./scripts/setup.ps1                  # if suitable Go is unavailable
./scripts/dev.ps1 bootstrap          # only if the CA has not been created yet
./scripts/dev.ps1 test
./.tools/go/bin/go.exe vet ./...
./scripts/setup.ps1 -WithRaceCompiler # if the race detector compiler is unavailable
./scripts/dev.ps1 race
node --test web/client_test.cjs web/admin_test.cjs
./scripts/dev.ps1 start
```

Build: `./scripts/dev.ps1 build`. Custom configuration: `./scripts/dev.ps1 start -Config config.android.json`. Use system `go` for `vet` if the local toolchain does not exist.

Files in `web/` are embedded in the binary with `go:embed`. After changing them, rebuild/restart the application and reload the browser page. There is no frontend bundler or hot reload.

## Project map

| Location | Responsibility |
| --- | --- |
| `cmd/pauseproxy/main.go` | Configuration, CA, rule loading, proxy and HTTP server setup, shutdown |
| `internal/config/` | Default startup configuration, IP → user mapping, addresses, time zone |
| `internal/sites/` | Rule format, domain normalization and validation, target selection, atomic persistence |
| `internal/state/` | In-memory sessions, daily usage, merging tab activity, runtime rule changes |
| `internal/httpapi/` | Client and admin APIs, PAC, public certificate, demo and howto |
| `internal/mitm/` | CA, request blocking, streaming, HTML injection and CSP adjustment |
| `web/` | Embedded HTML/CSS/JS and Node.js client tests |
| `scripts/`, `run_test.sh` | Toolchain setup and development commands |

Default ports: proxy **9080**, public server **9082**, settings **9083**. Settings defaults to HTTP on the trusted LAN and uses the configured `admin_password`; the password has no default and is never printed to the console.

## Request flow and MITM selection

PAC and HTTPS interception use the same current rule:

1. `bypass_domains`: the listed domain **and its subdomains** take precedence; PAC returns `DIRECT`.
2. Exact target domain of an enabled site: PAC returns the proxy and a new HTTPS tunnel uses MITM.
3. Other hosts: PAC returns `DIRECT`; with a manually configured explicit proxy, HTTPS remains an encrypted tunnel.

Do not introduce wildcards or implicitly include all subdomains as targets. Adding `youtube.com` must not intercept `googlevideo.com`, `r1.googlevideo.com` or an unknown YouTube subdomain. Presets are suggestions in settings, not CDN detection.

Exceptions are decided from the CONNECT host before MITM. A `video/*` response type does not replace this protection. Video on the same target domain as HTML cannot be separated by domain. Direct media links may work even when a site is blocked.

Rule changes update server-side PAC immediately. Browsers may use old PAC and open connections. During network testing, refresh PAC and close affected tabs/connections; an existing MITM tunnel cannot revert to a direct connection. Windows and Android procedures are in howto.

## Disk and RAM

- `config.dev.json`: configuration read at startup; `hosts` and the original limit seed migration if the rules file is missing.
- `.local/sites.json`: persistent rules, created on the first successful save. An existing file takes precedence, even with an empty site list.
- `.local/ca/`: public certificate and private key. Normal startup reuses them. Never automatically overwrite an invalid CA.
- `.tools/`, `dist/`: development toolchains, helper files and built binaries.
- RAM: sessions, usage, clicks, visits and diagnostic counters. Restart discards them. Resetting statistics preserves saved rules.

`sites_file` resolves relative to the startup configuration directory, or the working directory without `--config`. Configuration copies at the project root can therefore share `.local/sites.json`. Run only one instance against the same file; write serialization is within a process, not synchronization across processes. Restart to load manual rule edits.

Version 2 format (version 1 loads as independent sites without groups; loading writes nothing):

```json
{
  "version": 2,
  "groups": [{"id": "fun", "name": "Fun", "daily_limit_seconds": 1800}],
  "sites": [{
    "domain": "youtube.com",
    "domains": ["youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be"],
    "enabled": true,
    "mode": "limit",
    "daily_limit_seconds": 900,
    "group_id": "fun",
    "budget_mode": "capped"
  }],
  "bypass_domains": ["googlevideo.com", "ytimg.com"]
}
```

`domain` is a stable budget identifier and must be among the target domains. Mode is `limit` or `block`; limits range from 1 to 86400 seconds. The form accepts minutes and converts them to seconds. Maximums: 32 sites, 32 domains per site and 128 exceptions. Domains are lowercased, stripped of a trailing dot and converted using IDN. URLs, ports, IPs and wildcards are rejected. An exact domain can belong to only one site and must not be covered by an exception.

Rule changes run under a mutex: copy → modify → validate → temporary file and atomic replacement → publish in RAM. Windows uses `MoveFileEx`, Linux uses `Rename`. Write failures must preserve the original rules and PAC. An invalid existing file must stop startup rather than be replaced with defaults.

## Tracking and client

Groups are managed with `POST /api/groups` (`name`, `daily_limit_seconds`) and `PATCH/DELETE /api/groups/{id}`. The server generates IDs, which survive renaming. Token, IP, Host/Origin, JSON checks and atomic persistence match those for sites. At most 32 groups; names are nonempty and unique regardless of case. Sites have `group_id` and `budget_mode`: `shared`, `capped`, `independent`; ungrouped sites normalize to `independent`. Missing references are rejected.

`Snapshot.group_overview` includes `group_id`, `name`, `used_seconds`, `budget_seconds`, `remaining_seconds`, `state` for each user and group. `overview` rows additionally include `group_id`, `budget_mode`, `site_budget_seconds` and the `group_limit_reached` state. A site's `remaining_seconds` is its effective available time, the minimum of its own and group time for capped sites. Client status adds `group_id`, `group_name`, `group_remaining_seconds` (zero may be omitted), and `limit_scope` (`site` / `group`) when exhausted.

Group counters are a separate map keyed by user, stable group ID and local date. Each union of a site's sessions adds time exactly once; different sites add together. Rule changes first settle the previous assignments, so historical usage does not move. Group counters are cleaned up like site counters; reset and a new process clear them. Loading v1 migrates only in memory; the first explicit write saves v2. Deleting a group atomically converts members to independent budgets. A shared site's grayscale threshold that would be invalid after inheriting a limit stays saved, but the feature is disabled (the UI warns in the confirmation).

The server creates sessions and binds them to the user, IP, site and original host. A heartbeat cannot select another site. Budgets are keyed by **user + site identifier + local date**. Multiple IPs for the same user share a site's budget; concurrent tabs do not multiply it.

The client reports activity every 5 seconds. The server lease lasts 10 seconds. Active means a visible, focused tab with an interaction in the last minute. Video playback alone does not renew activity. Leaving sends an inactive report; if lost, at most the remaining lease is charged. Concurrent focus events are coalesced so active heartbeats do not hit the server's one-second rate limit.

Sequence numbers deduplicate clicks. Time splits at local midnight according to configuration. Limit changes and blocking apply at the next heartbeat and subsequent requests. Disabling a site or removing a domain stops tracking; the client removes the overlay and stops reporting. Removing and re-adding the same identifier during one run does not clear usage. Shared `blocked_hours` applies to all enabled sites.

## HTML, CSP and transport

HTML up to 4 MiB is processed, including a check after decompression; identity, gzip, deflate and Brotli are supported. Windows-1250 HTML (including cp1250 and x-cp1250 aliases) is converted to UTF-8 after decompression. Output Content-Type and meta charset declarations specify UTF-8; the 4 MiB limit also applies after conversion. Other responses are streamed. Upstream TLS is verified. Small request bodies up to 256 KiB support safe transport retries; there is no custom blanket retry of POSTs.

`mitm.NewUpstreamTransport` starts from the default Go transport, disables upstream proxy/environment routing and transparent decompression, and sets a 15-second response-header timeout. HTTP/2 uses a 15-second read-idle probe, a 5-second PING timeout and a 10-second write-progress timeout. Failed connections are retired by the transport; forwarding does not add its own retries. The v1.9.3 runtime source subset in `third_party/go-mitmproxy` adds only an optional `Options.UpstreamTransport` hook (see `PAUSEPROXY.md`). It is selected with a local module replacement; never edit the module cache.

`TestHTTP2BlackholeRecovery` runs a separate MITM proxy and a trusted local HTTP/2 server. After warming the first TLS connection, a listener silently drops its traffic without closing it. Health checks must retire that connection before the response-header timeout, and a subsequent request must succeed over a new upstream connection while reusing the browser connection. The POST case drops the response after the server receives the body and verifies that the operation is not replayed. `TestUpstreamHeaderTimeoutRecovery` also covers an unresponsive handler on HTTP/1.1 and HTTP/2. Tests use short probe intervals; production uses the values above. They do not use the running server, its ports, CA or rules.

The client is injected at the beginning of `head`, before site scripts. Break overlay styles are bundled with the injected client and need no extra network request. The overlay is initially visible until status confirms access; reloading with an exhausted limit preserves it. Embedded styles share the client's nonce; CSP rewriting preserves the effect of existing unsafe-inline. The client uses DOM APIs without `innerHTML`, so Trusted Types need not be removed. WebSocket, SSE and new service workers are rejected in the current MVP. At startup, the injected client unregisters old service workers for this origin. Controlled tabs may keep using a worker until closed; close them and reopen the site. A cached page without the client cannot run cleanup: use a fresh profile or DevTools → Application → Service Workers → Unregister and Ctrl+Shift+R. The client does not clear Cache Storage or cookies.

When adjusting CSP, preserve the page's effective permissions:

- A missing fetch directive without `default-src` is unrestricted. Do not add a restrictive `'self'`.
- A standalone `require-trusted-types-for 'script'` policy must not acquire new script, style or connection restrictions.
- A nonce suppresses otherwise effective `'unsafe-inline'`. Leave such a policy unchanged so original inline scripts keep working.
- For a restrictive policy, add the client nonce and preserve the site's existing nonces/hashes and sources. Check all CSP headers and meta policies, including report-only.

These cases have regression tests in `internal/mitm/addon_test.go`. A previous YouTube failure was caused by adding new CSP restrictions. Do not fix similar errors by removing CSP, Trusted Types or TLS verification entirely.

## API and security boundaries

The admin API requires the password in a Bearer header and checks Host/Origin independently of the proxy `users` mapping. Unregistered devices can open the login page and authenticate; the proxy and PAC still require a configured user. Unknown devices opening `/`, `/demo` or `/howto` on the public listener redirect to `admin_url`; API and PAC requests retain their 403 responses. Rules: `GET/POST /api/sites`, `PATCH/DELETE /api/sites/{domain}`, `GET/PUT /api/bypass-domains`. Legacy `PATCH /api/limits` returns 410. List bodies are limited to 64 KiB, others to 4 KiB; JSON is validated. `/howto` and `/connection` require no sign-in but retain Host/Origin checks; they contain neither a password nor a private key. Device registration still requires editing JSON and restarting.

Client endpoints live under `/__pauseproxy/` on the target origin and demo server. The demo can select a site with `?site=youtube.com`. Identity comes from the connection and configured IP mapping. For a phone, set `admin_addr` to the LAN interface or a wildcard, `admin_url` to the PC's HTTP address, and allow the device in `users`; see howto.

The MVP does not prevent deliberate circumvention: users can disable PAC and the page shares the client's origin. Do not commit admin passwords, CA keys, signed video URLs or users' network dumps.

## Verifying changes

Run relevant tests for the change, and before completing a functional change run the Go tests, vet, race detector and client tests listed above. Tests cover independent budgets, session unions, midnight, deduplication, runtime changes, rule validation and persistence, API protection, actual PAC evaluation with available Node.js, and HTTPS tunnels without a PauseProxy certificate/injection for exceptions.

For frontend changes, check the demo and settings in a browser. For CSP/transport changes, check the real target site, console and Network; a successful HTTP 200 with an injected client does not alone confirm playback. Real X/YouTube, caches, extensions and phones require manual checks.

For a concurrent test instance, use a separate configuration with three different ports and a separate `sites_file`; PAC must have the corresponding `advertised_proxy`. Do not stop unrelated processes. Only read a shared existing CA; do not copy its key into test fixtures.

## Known issues and diagnostics

As of October 4, 2026; these items are not a promise of a fix:

| Symptom | Interpretation / what to investigate |
| --- | --- |
| YouTube works, but `/api/stats/*` and `/ptracking` return 502 | Statistics requests fail. The console cannot distinguish a transport, upstream or response injection failure. Hypothesis to test: an empty response labeled HTML may be incorrectly passed to injection and changed to 502. Inspect the original status, Content-Type, body and server error. This issue has not been fixed or confirmed. |
| `googlevideo.com/videoplayback` returns 403 | The exception should go directly. Verify current PAC and old connections; the rejection may relate to the URL, player or extension. Do not infer the cause without the server response. |
| `context canceled` | May be normal request cancellation during navigation; frequent errors during stable playback need transport investigation. |
| Missing `[PauseProxy] Client started` | Check the exact target domain, PAC, cache/service worker, CSP and in-memory injection diagnostics in settings. |
| `about:blank` sandbox, manifest, install banner, LegacyDataMixin | YouTube/browser messages do not by themselves identify a PauseProxy defect. `xfready…` messages belong to Fready. |

Long stack traces often multiply console output. Group diagnostics by endpoint, status and main message; do not treat line count as the number of distinct errors. Remove tokens, cookies and signed URLs before sharing a dump. PauseProxy statistics are not intended as a network archive.

## Signing in and LAN settings

Set a nonempty `admin_password` in the configuration before startup. The example value is intentionally empty: there is no shared default password. Do not commit the real password. `bootstrap-ca` does not need it.

Settings uses HTTP on the trusted LAN by default and requires no browser certificate. Import the public CA from `ca_dir/ca-cert.pem` or download it from `http://127.0.0.1:9082/ca-cert.pem` before browsing proxied HTTPS sites. Explicit HTTPS admin URLs remain supported for existing configurations. The password stays only in page memory; reloading or signing out requires signing in again.

For a phone, set, for example, `admin_addr: "0.0.0.0:9083"` and `admin_url: "http://192.168.1.50:9083"`, using your PC's LAN IP. Add both phone and PC IPs to `users`. Use the exact `admin_url` on both devices. Allow firewall access to ports 9080, 9082 and 9083 only for the required devices. The `/howto#android` web guide contains a complete example and CA installation steps.

Settings has Overview, Sites & limits, Media exceptions and Connection tabs. The countdown uses `m:ss` / `h:mm:ss`; a separate gear opens `admin_url/#sites`.

`GET /api/state` additionally returns `date` (today in the configured time zone) and `overview`. Each row contains `user`, `site`, `used_seconds`, `budget_seconds`, `remaining_seconds` and `state` (`available`, `paused`, `always_blocked`, `scheduled_break`, `limit_reached`). Rows include all unique configured users and all current rules, sorted by user and then rule order. The budget includes granted bonuses only while extra time is enabled. The snapshot shares the client status calculation and creates no counters for inactive user/site pairs; `counters`, `limits`, `sessions`, `instance_id` and diagnostics remain compatible.

## Optional grayscale and extra time

For Daily limit rules, the site editor can enable grayscale after a configured number of active minutes (before the daily limit) and independently allow extra time. Both features default to off. Enabling grayscale for the first time suggests two thirds of the limit. Extra time defaults to 3 minutes and 1 use per day; both duration and count can be changed (1–100 uses).

After the limit runs out, the overlay and blocking page offer a button showing the bonus duration and remaining extensions. Extra time is charged only during activity, shared across the site's tabs and domains, and preserves grayscale. It does not override Always blocked or scheduled breaks. Media does not automatically resume after unblocking.

Version 1 rules added `grayscale_enabled`, `grayscale_after_seconds`, `extra_time_enabled`, `extra_time_seconds`, `extra_time_max_uses`. Older files retain their behavior. Client status adds `grayscale`, `extra_time_available`, `extra_time_seconds`, `extra_time_uses_remaining`. `POST /__pauseproxy/extra-time` accepts `session_id`, `instance_id`, `request_id`; the server checks identity and grants extra time atomically. Repeating the ID of a successful request on the same day does not grant another bonus.

Counts and bonus seconds stay only in RAM per user, site and local date. Midnight restores daily availability; reset and restart clear usage and bonuses. Removing and re-adding a site does not restore its use count. Changing the bonus duration affects future grants only; disabling bonuses removes their effect. Changes apply at the next heartbeat. After updating, rebuild and restart the application and reload open sites.

## Optional media blur

Daily limit rules also offer **Blur images/videos**, independent of grayscale. It applies a fixed CSS `blur(8px)` to `<img>` and `<video>` elements, including dynamically added media, after the site's own active-time threshold. Text and CSS backgrounds are unaffected. Both filters can be combined; extra time preserves both. Blur defaults to off, and first enabling it suggests two thirds of the site limit. There is no strength slider or reveal on hover.

Rules and POST/PATCH site APIs add `blur_enabled` and `blur_after_seconds`; client status adds `blur`. Omitted PATCH fields stay unchanged. Existing v1/v2 rules load with blur disabled; the saved document remains version 2. Enabled thresholds must be 1–86400 seconds and earlier than the site limit for independent/capped budgets. Shared budgets use the site's own time without requiring a threshold below the group limit. Removing a group disables blur if its threshold no longer fits the inherited site limit, preserving the threshold for editing.

The injected client loads `/__pauseproxy/blur.css` and toggles `data-pauseproxy-blur` on HTML. Like grayscale, the visual effect clears while blocked, offline or no longer tracked, and returns when an allowed status enables it. Settings apply at the next heartbeat. After updating, rebuild, restart PauseProxy and reload open sites.

## Daily limit editor and timeline

In the site editor, **Always block** replaces the Rule selector. Checking it hides the complete Daily limit section; unchecking restores its draft values. **Enabled** independently pauses or enables the saved rule. Saving still uses `mode: "block"` or `mode: "limit"`, with the existing validation and API format.

The Daily limit section puts the budget above a live timeline, followed by Grayscale, Blur images/videos and Extra time. The preview uses the unsaved settings, not today's usage. Blue means normal browsing, gray means grayscale, purple means blur and dark purple means both. Markers and a textual legend show exact filter start times and the daily limit. A dashed bonus section includes every permitted extension (for example, 70 minutes + 3 x 3 minutes = a maximum of 79 minutes). Bonuses require manual requests and preserve active filters. Invalid settings replace the chart with an explanation until corrected.

For capped budgets, the group may block access earlier. Shared budgets show the group's configured budget read-only and preview only the site's own filter thresholds, ending at the last filter start; this endpoint does not predict blocking because other sites also spend group time. With no filters, the shared preview shows explanatory text. Extra time is unavailable for shared budgets.

## Extra-time continuation methods

The Extra time card offers **Continue method**: **Click to extend** (the default for existing rules), **Math**, or **Reflection + 10-second pause**. Every extension requires a fresh task. Math uses multiplication with operands 2–12. Reflection requires at least three words and twelve letters, rejects simple repeated words/letters, and accepts Czech/Unicode text up to 500 characters. Start pause begins ten seconds; the browser counts only visible time. Continue explicitly requests the bonus after completion. Cancel discards the task without spending an extension. These forms appear in both the injected overlay and the blocking document; scheduled breaks and exhausted group budgets still cannot be extended.

Rules, administration APIs and client status add `extra_time_method` (`click`, `math`, `reflection`). Omitted PATCH fields stay unchanged and missing saved values default to `click`; document version remains 2. The editor's bonus legend describes the selected condition without changing the time scale.

`POST /__pauseproxy/extra-time/challenge` accepts the existing session/instance/request identifiers and returns a `challenge_id`, method, expiration, and math operands when applicable. Reflection creates its challenge on Start pause. A session holds at most one challenge in RAM, valid for five minutes and its local calendar day. Session expiry, reset/restart, method changes or lost eligibility invalidate it. The grant endpoint additionally accepts `challenge_id` and either integer `answer` or `reflection`. It validates the math or reflection text and at least ten seconds of server elapsed time for reflection; the client additionally requires ten visible seconds. Reflection answers are processed only during the request and are never retained or logged. Successful grants remain atomic and retries with the same request ID do not spend another extension.

## Per-site statistics in Sites & limits

Choose **Statistics for user** above the site table to view that user's live **Time remaining**. It uses the same effective site/group budget as Overview and refreshes every two seconds without replacing action buttons or the editor draft. The row's **Reset statistics** action resets today's used time, clicks, visits, bonus grants and bonus-use count only for the selected user and site. It cancels that user's sessions and pending challenges for the site; open pages reconnect automatically. Other users, sites, yesterday's counters and saved rules are unchanged. Already spent group time stays recorded, so resetting a shared/capped site cannot restore an exhausted group budget.

`POST /api/sites/{domain}/reset` accepts JSON `{"user":"configured user name"}` with the standard admin authentication and Origin/Host checks. Unknown users/sites are rejected. Reset affects only in-memory state and settles active usage before clearing the selected counters.
