# PauseProxy

Licensed under [MIT](LICENSE). Third-party components retain their own licenses;
see [licensing and dependency notices](LICENSING.md).

Local MVP: a Go application with selective HTTPS MITM, PAC, JavaScript heartbeats and site management. Statistics and sessions stay in RAM. Site rules are saved to `.local/sites.json`. The initial X rule retains its **120-second** limit; new sites default to **15 minutes**.

For code changes, see the [DEV_README.md developer guide](DEV_README.md): architecture, PAC/MITM invariants, tests, CSP and known issues.

## Local configuration

Before the first run, copy `config.dev.json` to `config.local.json` and set your own `admin_password` in the copy. Git ignores `config.local.json`, and the development scripts automatically prefer it. The template contains no password. The CA and rules remain in `.local/`.

## Quick start in Ubuntu WSL

In an Ubuntu terminal, change to the project directory (Windows drive C: is available under `/mnt/c/…`) and run:

```sh
bash ./run_test.sh
```

The script uses Linux Go ≥ 1.26. If unavailable, it downloads Go 1.27.1 from the official source and verifies the pinned SHA-256. It stores the Linux toolchain in `.tools/go-linux`, separately from Windows `.tools/go`. It supports amd64 and arm64, leaves the system PATH unchanged and does not run `sudo`.

It runs tests, `go vet` and the race detector, builds `dist/pauseproxy-linux`, and starts the application with `config.local.json` if present, otherwise with the `config.dev.json` template. It creates a missing CA once and reuses an existing CA without overwriting it. An incomplete or invalid CA causes an error. The application runs until you press **Ctrl+C**; the script stops only its own process.

```sh
bash ./run_test.sh --check       # Checks only; no server or CA bootstrap
bash ./run_test.sh --skip-race   # Explicitly skip the race detector
bash ./run_test.sh --help
```

Ubuntu requires `curl`, `python3` and `timeout` (coreutils), plus `tar` and `sha256sum` for downloads. The race detector requires GCC: `sudo apt update && sudo apt install build-essential`. The script reports missing dependencies without installing anything system-wide.

First stop any running Windows instance in its own terminal. Both instances use ports **9080/9082/9083** and may conflict in WSL mirrored mode. The script explains occupied ports without stopping other processes. Running `--check` also leaves an existing server unaffected.

After startup, open the [step-by-step setup guide](http://127.0.0.1:9083/howto) in **Windows Chrome or Edge**. It requires no sign-in and covers the demo, public CA import, PAC setup, testing X, troubleshooting and restoring the original settings. Settings also links to the guide. See [Microsoft's documentation](https://learn.microsoft.com/en-us/windows/wsl/networking) for Windows access to WSL through localhost.

For Chrome on Android, the guide has a [separate procedure for a phone over Wi-Fi](http://127.0.0.1:9083/howto#android): LAN configuration, WSL mirrored networking or running on Windows, firewall access for a specific phone, public CA import, Wi-Fi PAC, tracking checks and restoring the original settings.

The script can run from any working directory; it always reads settings from the project directory. An existing Windows CA in `.local/ca` can also be used in WSL, so no new import is needed if the CA is unchanged. Configure the Windows proxy and trusted certificate store manually as described in the guide.

## OpenWrt / Turris Omnia

The package, service and router tests live in the separate [PauseProxy-wrt](https://github.com/ja-pa/PauseProxy-wrt) repository. It builds this application from a pinned Git submodule.

## Quick start on Windows

In PowerShell at the project root:

```powershell
# Only if Go >= 1.26 is unavailable. Downloads verified portable Go into .tools.
./scripts/setup.ps1

# Creates the CA and its private key in .local/ca once.
./scripts/dev.ps1 bootstrap

# Normal startup only reads the CA. Stop with Ctrl+C.
./scripts/dev.ps1 start
```

If PowerShell blocks scripts, run the specific command with `powershell -ExecutionPolicy Bypass -File .\scripts\dev.ps1 start`. No system-wide execution policy change is required.

- Settings: <http://127.0.0.1:9083>
- Demo without PAC or CA installation: <http://127.0.0.1:9082/demo>
- PAC: <http://127.0.0.1:9082/proxy.pac>
- Proxy: `127.0.0.1:9080`

Sign in to settings with **the password configured in `admin_password`**. It stays only in page memory and uses neither cookies nor localStorage. The demo spends the selected site's budget (X by default): click the button, open a second tab, hide both tabs or adjust the site's minutes in settings. Statistics refresh every two seconds. Reset and restart preserve saved site rules and limits; restart clears statistics.

With Go already available (Windows or Linux):

```sh
go run ./cmd/pauseproxy bootstrap-ca --config ./config.local.json
go run ./cmd/pauseproxy --config ./config.local.json
go test ./...
go vet ./...
go build -o pauseproxy ./cmd/pauseproxy
```

## Persistent site rules and media exceptions

In settings, add the primary domain, exact target domains, daily minutes, Daily limit / Always blocked mode and enabled state. A site's domains share a budget; each user has their own usage. The X and YouTube presets only fill in a suggestion, which is saved with Save. YouTube suggests `youtube.com`, `www.youtube.com`, `m.youtube.com`, `youtu.be` and the exceptions `googlevideo.com`, `ytimg.com`. X suggests `x.com`, `www.x.com` and the exception `twimg.com`.

Media exceptions include the domain and its subdomains and take precedence. Conflicting target domains are rejected. Unknown subdomains are not MITM targets. Video served directly from a target domain cannot be separated by hostname. Direct media links may remain accessible even when the page is blocked.

`sites_file` defaults to `.local/sites.json`, relative to the startup configuration directory (or the working directory without `--config`). Configurations at the project root share the same file. A missing file is created on the first save; an existing file takes precedence, including an empty list. An invalid file stops startup and is not overwritten. Changes use atomic replacement; a write failure preserves the in-memory rules and PAC. One running instance is supported; restart to load manual edits.

After domain changes, the server's PAC is current immediately, but browsers keep their own PAC context. On Windows, turn the setup script off and on with the same URL; in Android Wi-Fi settings, change Proxy to None and then restore automatic configuration. Close affected tabs; after changing exceptions, close old connections or restart the browser. An existing MITM tunnel cannot become a direct connection. See [Chromium PAC behavior](https://chromium.googlesource.com/chromium/src.git/+/refs/heads/lkgr/net/docs/proxy.md).

Removing or disabling a rule/domain stops tracking and removes the overlay. Statistics are retained; a site re-added during the same run keeps today's usage. Resetting statistics preserves saved rules. The demo lets you select a site.

List APIs accept up to 64 KiB of JSON; other APIs accept 4 KiB. Limits: 32 sites, 32 exact domains per site and 128 exceptions. New rules default to 900 seconds; POST can also atomically add a preset's `bypass_domains`. PATCH changes target domains, enabled state, mode and the limit in seconds; the primary identifier cannot be renamed.

## Chrome and X

1. Try the demo first. It needs no system changes.
2. On Windows, open `.local/ca/ca-cert.pem` and manually import the **public certificate** into the current user's trusted root authorities. Alternatively, explicitly run `certutil -user -addstore Root .local/ca/ca-cert.pem`. Never import or share `ca-key.pem`. Bootstrap restricts CA directory access to the current user with Windows ACLs; Linux uses 0700/0600 permissions.
3. Windows Settings → Network & internet → Proxy → Use setup script: `http://127.0.0.1:9082/proxy.pac`.
4. Test with a fresh Chrome profile without a previously registered X service worker. Alternatively, use Unregister in DevTools → Application → Service Workers. The proxy rejects new service workers. The injected client unregisters old registrations for that origin; close existing tabs and reopen the site. If a cached page loads without the client, unregister the worker manually in DevTools → Application → Service Workers → Unregister and use Ctrl+Shift+R.
5. Open `https://x.com`. Verify the countdown, clicks in settings, warning, overlay in an open tab and 403 on a new navigation after the budget runs out.
6. After the experiment, disable the proxy setup script. Remove the CA in `certmgr.msc` under **PauseProxy Development CA** if desired. The application does not change system settings or the trust store itself.

PAC proxies only exact enabled target domains. The default X rule has `x.com` and `www.x.com`; `api.x.com` and other subdomains are not included automatically. `pbs.twimg.com`, `video.twimg.com` and other non-target sites return DIRECT. A non-target HTTPS host sent through an explicit proxy remains an encrypted tunnel. Upstream TLS is verified. The [go-mitmproxy interface](https://github.com/lqqyt2423/go-mitmproxy/tree/v1.9.3) is pinned to v1.9.3.

## Configuration and a phone over LAN

`config.dev.json` is only read at startup. Without `--config`, the same built-in defaults apply. Each added site has a separate budget for each user. Random budgets are outside this MVP. PauseProxy-wrt maintains the OpenWrt integration.

For LAN use, create your own configuration copy:

- `proxy_addr`: `0.0.0.0:9080`, `pac_addr`: `0.0.0.0:9082`.
- `advertised_proxy`: the PC's actual IP, e.g. `192.168.1.50:9080`.
- Add the phone's exact IP to `users`, e.g. `"192.168.1.70": "dev"`. Multiple IPs for one user share a budget. Identity comes from the connection; the client cannot choose it.
- Locally, keep `admin_addr` at `127.0.0.1:9083` and `admin_url` at `http://127.0.0.1:9083`. For LAN access, configure the listener and the PC's HTTP address. Settings login is available to unregistered devices; admin APIs require the admin password and matching Host/Origin. The `users` mapping controls proxy and PAC access. An unknown device opening `/`, `/demo` or `/howto` on the PAC server is redirected to `admin_url`. Add device IPs to `users` in the JSON configuration and restart to enable proxy access; device registration is not yet available in the admin UI.
- Allow ports 9080 and 9082 through the firewall only on a private network and from the test IPs.
- On the phone, use PAC `http://192.168.1.50:9082/proxy.pac` and manually trust the public CA. PAC and trust options vary between Android versions and applications; the target is Chrome over Wi-Fi.

Replace `0.0.0.0` in links with the actual LAN IP. `hosts` and `daily_limit_seconds` seed the initial rules when `sites_file` does not exist. Current rules control both PAC and MITM. The limit is 1–86400 seconds. `blocked_hours` accepts, for example, `[{"start":"22:00","end":"06:00"}]`; it uses `Europe/Prague` and applies to every enabled site.

## State, API and disk

Time is the union of a user's active sessions for a particular site; a tab must be visible, focused and have an interaction within the last 60 seconds. Pointer movement, clicks, keyboard input, touch or scrolling renew the one-minute interval. After it expires, “paused” appears; another interaction or returning to the window resumes tracking. Video playback alone does not renew the interval. Losing window focus or hiding the tab stops tracking. Concurrent tabs do not multiply usage. Heartbeats arrive every 5 seconds and renew a 10-second lease. The first heartbeat does not charge past time. Hiding a tab sends a final report; a lost report can add at most the remaining 10 seconds. Daily accounting splits at local midnight. Clicks are aggregated `isTrusted` events outside the PauseProxy interface; retrying a batch does not add clicks again.

RAM holds at most 256 sessions expiring after 5 minutes, aggregates for today and yesterday, limits and at most 128 host certificates. Click content, targets, coordinates and URL history are not retained. Statistics, sessions and operational logs are not persisted. Only explicitly saving rules writes `sites_file`. HTML is processed in RAM with a 4 MiB limit (compressed and decompressed); other responses are streamed. WebSocket and SSE are rejected in this MVP because the upstream library retains their history in memory.

Upstream requests use a shared HTTP transport that can establish a new connection after a failure independently of the browser connection. Bodies up to 256 KiB are briefly buffered only in RAM so Go can reread them for safe transport retries; larger bodies are streamed. There is no custom automatic retry of POST requests. `context canceled` can occur when the browser cancels loading; it does not itself mean the proxy crashed. Repeated errors during normal loading or `cannot rewind body after connection loss` require transport investigation.

The shared transport checks silent HTTP/2 connections after 15 seconds and closes them if a PING has no reply within 5 seconds. Writes with no progress time out after 10 seconds; waiting for response headers after sending a request is limited to 15 seconds. This retires stale connections after sleep or a network change without restarting PauseProxy or clearing statistics. A request already in flight may return 502; subsequent requests reconnect. Ambiguous POSTs are not automatically replayed. These are phase timeouts, not a total response-body download limit. The local `third_party/go-mitmproxy` runtime source patch exposes the transport configuration hook.

The normal process only reads the CA and startup configuration. It writes site rules when explicitly saved in settings; CA bootstrap is a separate write. Statistics and sessions are not persisted. Builds, Go caches and browser caches are outside this guarantee.

| Interface | Endpoint | Protection |
| --- | --- | --- |
| X / demo origin | `GET /__pauseproxy/client.js` | Known IP |
| X / demo origin | `POST /__pauseproxy/session` | Known IP, JSON, Origin check |
| X / demo origin | `POST /__pauseproxy/heartbeat` | Session bound to IP and user, instance, sequence |
| X / demo origin | `GET /__pauseproxy/status` | Known IP |
| PAC server | `GET /proxy.pac`, `/healthz`, `/ca-cert.pem`, `/demo` | Known IP, public CA only |
| Settings | `GET /api/state` | HTTP on trusted LAN, exact Host/Origin, password in Bearer header |
| Settings | `GET/POST /api/sites`, `PATCH/DELETE /api/sites/{domain}` | Token, Host/Origin and JSON |
| Settings | `GET/PUT /api/bypass-domains` | Same; media exceptions |
| Settings | `PATCH /api/limits` | 410: use the site editor |
| Settings | `POST /api/reset` | Same, plus JSON `{}` |

All state APIs use no-store, a 4 KiB JSON body limit (64 KiB for rule list APIs), and no CORS for mutations. The session ID is a random token; after reset/restart, the client creates a new session and discards old unsent batches. Losing the client API for 10 seconds triggers the overlay. Raising the limit restores an open tab at the next heartbeat; reload a blocking page.

## Verification

```powershell
./scripts/dev.ps1 test
./.tools/go/bin/go.exe vet ./...
# The Windows race detector needs a C compiler; portable option:
./scripts/setup.ps1 -WithRaceCompiler
./scripts/dev.ps1 race
./scripts/dev.ps1 build
./dist/pauseproxy.exe --config ./config.local.json
```

On Linux with GCC: `go test -race ./...`.

Automated tests cover concurrency, hiding, expiration, limit changes, midnight, reset, deduplication, IP binding, capacity, API protection, the allowlist, compression/CSP, injection failures and HTTPS CONNECT with a trusted test CA. `go vet` checks the sources.

Manual checks for real Chrome/X/phone use: SPA navigation, Back, cache/service workers, phone sleep, direct media routing and failure behavior. To audit writes, use Process Monitor filtered to `pauseproxy.exe`, run the binary after bootstrap and inspect WriteFile operations. CA file tests do not replace this audit.

The MVP does not prevent deliberate circumvention: PAC can be disabled and the script shares X's origin. Current X, its CSP, API domains and Android require manual verification; the proxy cannot guarantee blocking a cached page without the injected client. PauseProxy-wrt maintains the OpenWrt package; persistent statistics are not implemented.

The client is injected at the beginning of HTML head with a CSP nonce, before site scripts. It builds the DOM without `innerHTML`, so it needs no Trusted Types exception. It sends a heartbeat every 5 seconds and charges only a visible, focused tab with activity in the last minute. Settings shows in-memory diagnostics for injections, created sessions and received heartbeats over the process lifetime (resetting statistics does not clear these). After updating, restart the application and reload X with Ctrl+Shift+R. The console should show `[PauseProxy] Client started`; if absent and injection counts do not increase, check PAC and cache/service workers in a fresh profile.

Run JavaScript tests with Node.js: `node --test web/client_test.cjs web/admin_test.cjs`. Client tests use controlled clocks to check one-minute inactivity, real versus synthetic interactions, focus, hiding, history restoration, blocking and clicks without waiting a minute. Admin tests check the rule editor, presets, validation, protection of unsaved changes, saving and list actions; no extra packages are needed.

## Signing in and LAN settings

Set a nonempty `admin_password` in the configuration before startup. The example value is intentionally empty: there is no shared default password. Do not commit the real password. `bootstrap-ca` does not need it.

Settings uses HTTP on the trusted LAN by default and requires no browser certificate. Import the public CA from `ca_dir/ca-cert.pem` or download it from `http://127.0.0.1:9082/ca-cert.pem` before browsing proxied HTTPS sites. Explicit HTTPS admin URLs remain supported for existing configurations. The password stays only in page memory; reloading or signing out requires signing in again.

For a phone, set, for example, `admin_addr: "0.0.0.0:9083"` and `admin_url: "http://192.168.1.50:9083"`, using your PC's LAN IP. Add both phone and PC IPs to `users`. Use the exact `admin_url` on both devices. Allow firewall access to ports 9080, 9082 and 9083 only for the required devices. The `/howto#android` web guide contains a complete example and CA installation steps.

Settings has Overview, Sites & limits, Media exceptions and Connection tabs. The countdown uses `m:ss` / `h:mm:ss`; a separate gear opens `admin_url/#sites`.

Overview shows today's remaining budget for every configured site and user, including sites without activity. The usage bar shrinks, turns orange at 20% or less and displays Daily limit reached when exhausted. It includes granted extra time. Always blocked, Paused and Scheduled break distinguish rule states. The server's time zone determines the date; the overview refreshes every two seconds. Clicks and visits are hidden in Overview but are still measured and available through the API.

In Sites & limits, **+ Add site** or **✎** opens an editor below the list. **⏸ / ▶** pauses or enables a rule; **🗑** removes it after confirmation. Numeric Grayscale and Extra time inputs appear only when enabled. Domains and suggested media exceptions are under Advanced domains. Switching tabs preserves the draft; Cancel or opening another rule asks before discarding changes.

## Optional grayscale and extra time

For Daily limit rules, the site editor can enable grayscale after a configured number of active minutes (before the daily limit) and independently allow extra time. Both features default to off. Enabling grayscale for the first time suggests two thirds of the limit. Extra time defaults to 3 minutes and 1 use per day; both duration and count can be changed (1–100 uses).

After the limit runs out, the overlay and blocking page offer a button showing the bonus duration and remaining extensions. Extra time is charged only during activity, shared across the site's tabs and domains, and preserves grayscale. It does not override Always blocked or scheduled breaks. Media does not automatically resume after unblocking.

Version 1 rules added `grayscale_enabled`, `grayscale_after_seconds`, `extra_time_enabled`, `extra_time_seconds`, `extra_time_max_uses`. Older files retain their behavior. Client status adds `grayscale`, `extra_time_available`, `extra_time_seconds`, `extra_time_uses_remaining`. `POST /__pauseproxy/extra-time` accepts `session_id`, `instance_id`, `request_id`; the server checks identity and grants extra time atomically. Repeating the ID of a successful request on the same day does not grant another bonus.

Counts and bonus seconds stay only in RAM per user, site and local date. Midnight restores daily availability; reset and restart clear usage and bonuses. Removing and re-adding a site does not restore its use count. Changing the bonus duration affects future grants only; disabling bonuses removes their effect. Changes apply at the next heartbeat. After updating, rebuild and restart the application and reload open sites.

## Web interface styles

Settings, the guide and demo use a local copy of [MVP.css](https://andybrewer.github.io/mvp/) in `web/mvp.css` (v1.18, MIT; license in `web/MVP-LICENSE.txt`). The file is unchanged; layout additions live in `web/app.css`. CSS and the license are embedded in the Go binary; npm, a CDN and network stylesheet loading are unnecessary. When updating MVP, download the CSS and license from upstream and verify all three pages.

## Groups and shared budgets

In Sites & limits, create a group with **+ Add group** (default limit: 15 minutes). In the site editor, select a group and **Shared group budget**, **Group budget + site limit** or **Independent budget**. A shared site spends only the group budget; a capped site is constrained by both its own and the group budget; an independent site is listed in the group but does not spend its time. Each user has separate budgets. Concurrent activity on different sites adds to group usage; multiple tabs for the same site count once.

Grayscale uses the site's own usage today. Extra time is available only for independent or capped budgets; it cannot override an exhausted group and still spends group time. Groups have no separate bonus. Moving between groups or changing modes affects accounting only after saving. Earlier usage remains with the original group; the site's own limit considers the whole day. Renaming or changing a limit does not clear usage.

Deleting a group preserves its sites as independent rules. Shared sites inherit the group's base limit; others retain their own. If a shared site's grayscale threshold is not below the inherited limit, the confirmation warns about this and grayscale is disabled; the threshold remains saved. Overview shows a shared bar and expandable members with their own usage and mode. Group blocking messages include the group name.

Rules are now saved as version 2. Version 1 is migrated in memory without changing behavior; version 2 is written to disk only when rules are explicitly saved. Older binaries cannot read version 2. Statistics, including groups, stay only in RAM; reset, restart and a new local day restore the available base budgets.

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
