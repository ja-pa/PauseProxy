#!/usr/bin/env bash
# Local development only: builds/toolchains may write; runtime statistics stay in RAM.
set -Eeuo pipefail

fail() { printf '\nPauseProxy: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'EOF'
Usage: bash ./run_test.sh [--check] [--skip-race] [--help]
  no options  Check the project, build the Linux application and start it. Stop: Ctrl+C.
  --check     Tests, go vet and race detector only; no server or CA bootstrap.
  --skip-race Explicitly skip the race detector (e.g. if GCC is unavailable).
  --help      Show this help.

Uses config.local.json if present, otherwise config.dev.json; downloads verified Go into .tools/go-linux if needed.
Leaves system PATH, Windows proxy and the trusted certificate store unchanged.
EOF
}
check_only=false
skip_race=false
for arg in "$@"; do
  case "$arg" in
    --check) check_only=true ;;
    --skip-race) skip_race=true ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; fail "Unknown option: $arg" ;;
  esac
done
project_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
cd -- "$project_root"
config_file=config.dev.json
[[ ! -f config.local.json ]] || config_file=config.local.json
[[ $(uname -s) == Linux ]] || fail 'Run this script in Ubuntu WSL or another Linux environment.'
case $(uname -m) in
  x86_64) arch=amd64; sha=63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445 ;;
  aarch64|arm64) arch=arm64; sha=3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec ;;
  *) fail 'Supported architectures: Linux amd64 and arm64.' ;;
esac
for dep in curl timeout python3; do
  command -v "$dep" >/dev/null || fail "Missing $dep. On Ubuntu, run: sudo apt update && sudo apt install curl coreutils python3"
done
if ! $skip_race && ! command -v gcc >/dev/null; then
  fail 'The race detector requires GCC. Run: sudo apt update && sudo apt install build-essential; or choose --skip-race.'
fi

# Parse values as data, never source/eval JSON. Use the configured bind addresses.
connection_info=$(python3 - "$config_file" <<'PY'
import json, sys
from urllib.parse import urlsplit
with open(sys.argv[1], encoding='utf-8') as f:
    c = json.load(f)
for key in ('proxy_addr', 'pac_addr', 'admin_addr'):
    address = c[key]
    u = urlsplit('http://' + address)
    host = u.hostname
    if not host or not u.port:
        raise ValueError('Invalid address: ' + address)
    host = '127.0.0.1' if host in ('0.0.0.0', '::') else host
    authority = ('[' + host + ']' if ':' in host else host) + ':' + str(u.port)
    print(host)
    print(u.port)
    print(c.get('admin_url', 'https://' + authority) if key == 'admin_addr' else 'http://' + authority)
ca = c.get('ca_dir', '.local/ca')
if not isinstance(ca, str) or not ca or '\n' in ca or '\r' in ca:
    raise ValueError('Invalid ca_dir')
print(ca)
PY
)
mapfile -t connection <<< "$connection_info"
proxy_host=${connection[0]}; proxy_port=${connection[1]}
pac_host=${connection[3]}; pac_port=${connection[4]}; pac_url=${connection[5]}
admin_host=${connection[6]}; admin_port=${connection[7]}; admin_url=${connection[8]}
ca_dir=${connection[9]}
port_check() {
  local host=$1 port=$2
  if timeout 1 bash -c 'exec 3<>/dev/tcp/$1/$2' _ "$host" "$port" 2>/dev/null; then
    fail "Port $host:$port is occupied. A Windows PauseProxy instance may still be running. Stop it in its terminal with Ctrl+C and rerun this script. No process was stopped."
  fi
}
if ! $check_only; then
  port_check "$proxy_host" "$proxy_port"
  port_check "$pac_host" "$pac_port"
  port_check "$admin_host" "$admin_port"
fi

download_dir=''
app_pid=''
cleanup() {
  local result=$?
  trap - EXIT
  if [[ -n $app_pid ]] && kill -0 "$app_pid" 2>/dev/null; then
    # The child explicitly handles SIGINT; never kill processes by name or port.
    kill -INT "$app_pid" 2>/dev/null || true
    for ((i=0; i<40; i++)); do
      kill -0 "$app_pid" 2>/dev/null || break
      sleep 0.1
    done
    kill -TERM "$app_pid" 2>/dev/null || true
    wait "$app_pid" 2>/dev/null || true
  fi
  # Only remove the specific temporary directory created by this invocation.
  if [[ -n $download_dir && $download_dir == "$project_root/.tools/go-linux/.download."* ]]; then
    rm -rf -- "$download_dir"
  fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

usable_go() {
  local candidate=$1 version major minor
  [[ -x $candidate ]] || return 1
  [[ $(GOTOOLCHAIN=local "$candidate" env GOHOSTOS 2>/dev/null) == linux ]] || return 1
  version=$(GOTOOLCHAIN=local "$candidate" version 2>/dev/null) || return 1
  [[ $version =~ go([0-9]+)\.([0-9]+) ]] || return 1
  major=${BASH_REMATCH[1]}; minor=${BASH_REMATCH[2]}
  ((major > 1 || (major == 1 && minor >= 26)))
}
unset GOROOT # Ignore a Windows GOROOT inherited into WSL.
go_bin=$(command -v go || true)
if ! usable_go "$go_bin"; then
  go_bin="$project_root/.tools/go-linux/go/bin/go"
  if ! usable_go "$go_bin"; then
    for dep in tar sha256sum mktemp; do command -v "$dep" >/dev/null || fail "Missing $dep; install tar and coreutils."; done
    [[ ! -e .tools/go-linux/go ]] || fail 'Local Go is incomplete or too old. Check .tools/go-linux/go; the script will not overwrite it automatically.'
    mkdir -p .tools/go-linux
    download_dir=$(mktemp -d "$project_root/.tools/go-linux/.download.XXXXXX")
    printf '\nDownloading Go 1.27.1 for Linux %s (project-local only)...\n' "$arch"
    curl --fail --location --retry 2 --connect-timeout 15 --max-time 300 \
      "https://go.dev/dl/go1.27.1.linux-$arch.tar.gz" -o "$download_dir/go.tar.gz"
    printf '%s  %s\n' "$sha" "$download_dir/go.tar.gz" | sha256sum --check --status || fail 'Go archive SHA-256 mismatch.'
    tar -xzf "$download_dir/go.tar.gz" -C "$download_dir"
    mv -- "$download_dir/go" .tools/go-linux/go
    usable_go "$go_bin" || fail 'Cannot run the downloaded Linux toolchain.'
    rm -rf -- "$download_dir"
    download_dir=''
  fi
fi
export GOTOOLCHAIN=local
export GOOS=linux GOARCH="$arch"
printf '\nToolchain: '; "$go_bin" version
printf '\n[1/3] Tests\n'; "$go_bin" test ./...
printf '\n[2/3] Static analysis\n'; "$go_bin" vet ./...
if $skip_race; then
  printf '\n[3/3] Race detector explicitly skipped (--skip-race).\n'
else
  printf '\n[3/3] Race detector\n'; CGO_ENABLED=1 CC=gcc "$go_bin" test -race ./...
fi
if $check_only; then printf '\nChecks complete. Neither the server nor CA bootstrap was started.\n'; exit 0; fi

printf '\nBuilding the Linux application...\n'
mkdir -p dist
CGO_ENABLED=0 "$go_bin" build -o dist/pauseproxy-linux ./cmd/pauseproxy
if [[ ! -e $ca_dir && ! -L $ca_dir ]]; then
  printf '\nCreating the CA in %s for the first time...\n' "$ca_dir"
  ./dist/pauseproxy-linux bootstrap-ca --config "$config_file"
else
  [[ -f $ca_dir/ca-cert.pem && -f $ca_dir/ca-key.pem ]] || fail "The CA in $ca_dir is incomplete. The script will not overwrite it; check ca-cert.pem and ca-key.pem."
  printf '\nReusing the existing CA in %s; files will not be overwritten.\n' "$ca_dir"
fi
# Recheck after tests/build in case another instance started in the meantime.
port_check "$proxy_host" "$proxy_port"
port_check "$pac_host" "$pac_port"
port_check "$admin_host" "$admin_port"
./dist/pauseproxy-linux --config "$config_file" &
app_pid=$!
ready=false
for ((i=0; i<40; i++)); do
  if ! kill -0 "$app_pid" 2>/dev/null; then
    wait "$app_pid" || fail "Application startup failed. If it reports a CA error, the existing files were not overwritten."
    fail 'The application exited prematurely.'
  fi
  if curl --noproxy '*' --fail --silent --max-time 1 "$pac_url/healthz" >/dev/null && \
     curl --noproxy '*' --cacert "$ca_dir/ca-cert.pem" --fail --silent --max-time 1 "$admin_url/howto" >/dev/null && \
     curl --noproxy '*' --fail --silent --max-time 1 "$pac_url/proxy.pac" >/dev/null; then
    ready=true; break
  fi
  sleep 0.25
done
$ready || fail 'HTTP servers are unavailable. Check the addresses and users in the configuration.'
printf '\nReady. Open in your Windows browser:\n  Guide:    %s/howto\n  Settings: %s\n  Demo:     %s/demo\n  PAC:      %s/proxy.pac\n\nSettings uses admin_password from the configuration. Import the public CA before opening HTTPS settings. Stop: Ctrl+C. Statistics remain only in RAM.\n' "$admin_url" "$admin_url" "$pac_url" "$pac_url"
wait "$app_pid"
