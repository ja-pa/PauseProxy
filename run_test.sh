#!/usr/bin/env bash
# Local development only: builds/toolchains may write; runtime statistics stay in RAM.
set -Eeuo pipefail

fail() { printf '\nPauseProxy: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'EOF'
PouĹľitĂ­: bash ./run_test.sh [--check] [--skip-race] [--help]
  bez voleb    OvÄ›Ĺ™Ă­ projekt, sestavĂ­ Linux aplikaci a spustĂ­ ji. UkonÄŤenĂ­: Ctrl+C.
  --check      Jen testy, go vet a race detector; bez serveru a bootstrapu CA.
  --skip-race  ExplicitnÄ› vynechĂˇ race detector (napĹ™. pokud chybĂ­ GCC).
  --help       Tento nĂˇvod.

PouĹľĂ­vĂˇ config.dev.json a pĹ™Ă­padnÄ› stĂˇhne ovÄ›Ĺ™enĂ© Go do .tools/go-linux.
NemÄ›nĂ­ systĂ©movĂ˝ PATH, Windows proxy ani ĂşloĹľiĹˇtÄ› dĹŻvÄ›ryhodnĂ˝ch certifikĂˇtĹŻ.
EOF
}
check_only=false
skip_race=false
for arg in "$@"; do
  case "$arg" in
    --check) check_only=true ;;
    --skip-race) skip_race=true ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; fail "NeznĂˇmĂˇ volba: $arg" ;;
  esac
done
project_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
cd -- "$project_root"
config_file=config.dev.json
[[ ! -f config.local.json ]] || config_file=config.local.json
[[ $(uname -s) == Linux ]] || fail 'SpusĹĄ skript v Ubuntu WSL nebo jinĂ©m Linuxu.'
case $(uname -m) in
  x86_64) arch=amd64; sha=63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445 ;;
  aarch64|arm64) arch=arm64; sha=3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec ;;
  *) fail 'PodporovanĂ© architektury: Linux amd64 a arm64.' ;;
esac
for dep in curl timeout python3; do
  command -v "$dep" >/dev/null || fail "ChybĂ­ $dep. V Ubuntu spusĹĄ: sudo apt update && sudo apt install curl coreutils python3"
done
if ! $skip_race && ! command -v gcc >/dev/null; then
  fail 'Race detector vyĹľaduje GCC. SpusĹĄ: sudo apt update && sudo apt install build-essential; nebo zvol --skip-race.'
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
        raise ValueError('NeplatnĂˇ adresa: ' + address)
    host = '127.0.0.1' if host in ('0.0.0.0', '::') else host
    authority = ('[' + host + ']' if ':' in host else host) + ':' + str(u.port)
    print(host)
    print(u.port)
    print(c.get('admin_url', 'https://' + authority) if key == 'admin_addr' else 'http://' + authority)
ca = c.get('ca_dir', '.local/ca')
if not isinstance(ca, str) or not ca or '\n' in ca or '\r' in ca:
    raise ValueError('NeplatnĂ˝ ca_dir')
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
    fail "Port $host:$port je obsazenĂ˝. MĹŻĹľe stĂˇle bÄ›Ĺľet Windows verze PauseProxy. UkonÄŤi ji v jejĂ­m terminĂˇlu pomocĂ­ Ctrl+C a spusĹĄ skript znovu. Ĺ˝ĂˇdnĂ˝ proces nebyl ukonÄŤen."
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
    for dep in tar sha256sum mktemp; do command -v "$dep" >/dev/null || fail "ChybĂ­ $dep; nainstaluj tar a coreutils."; done
    [[ ! -e .tools/go-linux/go ]] || fail 'LokĂˇlnĂ­ Go je neĂşplnĂ© nebo pĹ™Ă­liĹˇ starĂ©. Zkontroluj .tools/go-linux/go; skript je automaticky nepĹ™epĂ­Ĺˇe.'
    mkdir -p .tools/go-linux
    download_dir=$(mktemp -d "$project_root/.tools/go-linux/.download.XXXXXX")
    printf '\nStahuji Go 1.27.1 pro Linux %s (pouze do projektu)â€¦\n' "$arch"
    curl --fail --location --retry 2 --connect-timeout 15 --max-time 300 \
      "https://go.dev/dl/go1.27.1.linux-$arch.tar.gz" -o "$download_dir/go.tar.gz"
    printf '%s  %s\n' "$sha" "$download_dir/go.tar.gz" | sha256sum --check --status || fail 'SHA-256 Go archivu nesouhlasĂ­.'
    tar -xzf "$download_dir/go.tar.gz" -C "$download_dir"
    mv -- "$download_dir/go" .tools/go-linux/go
    usable_go "$go_bin" || fail 'StaĹľenĂ˝ Linux toolchain nelze spustit.'
    rm -rf -- "$download_dir"
    download_dir=''
  fi
fi
export GOTOOLCHAIN=local
export GOOS=linux GOARCH="$arch"
printf '\nToolchain: '; "$go_bin" version
printf '\n[1/3] Testy\n'; "$go_bin" test ./...
printf '\n[2/3] StatickĂˇ kontrola\n'; "$go_bin" vet ./...
if $skip_race; then
  printf '\n[3/3] Race detector vĂ˝slovnÄ› vynechĂˇn (--skip-race).\n'
else
  printf '\n[3/3] Race detector\n'; CGO_ENABLED=1 CC=gcc "$go_bin" test -race ./...
fi
if $check_only; then printf '\nKontroly dokonÄŤeny. Server ani bootstrap CA nebyly spuĹˇtÄ›ny.\n'; exit 0; fi

printf '\nSestavuji Linux aplikaciâ€¦\n'
mkdir -p dist
CGO_ENABLED=0 "$go_bin" build -o dist/pauseproxy-linux ./cmd/pauseproxy
if [[ ! -e $ca_dir && ! -L $ca_dir ]]; then
  printf '\nJednorĂˇzovÄ› vytvĂˇĹ™Ă­m CA v %sâ€¦\n' "$ca_dir"
  ./dist/pauseproxy-linux bootstrap-ca --config "$config_file"
else
  [[ -f $ca_dir/ca-cert.pem && -f $ca_dir/ca-key.pem ]] || fail "CA v $ca_dir je neĂşplnĂˇ. Skript ji nepĹ™epĂ­Ĺˇe; zkontroluj ca-cert.pem a ca-key.pem."
  printf '\nPouĹľĂ­vĂˇm existujĂ­cĂ­ CA v %s; soubory nepĹ™episuji.\n' "$ca_dir"
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
    wait "$app_pid" || fail "Aplikace se nespustila. Pokud hlĂˇsĂ­ chybu CA, existujĂ­cĂ­ soubory nebyly pĹ™epsĂˇny."
    fail 'Aplikace se pĹ™edÄŤasnÄ› ukonÄŤila.'
  fi
  if curl --noproxy '*' --fail --silent --max-time 1 "$pac_url/healthz" >/dev/null && \
     curl --noproxy '*' --cacert "$ca_dir/ca-cert.pem" --fail --silent --max-time 1 "$admin_url/howto" >/dev/null && \
     curl --noproxy '*' --fail --silent --max-time 1 "$pac_url/proxy.pac" >/dev/null; then
    ready=true; break
  fi
  sleep 0.25
done
$ready || fail 'HTTP servery nejsou dostupnĂ©. Zkontroluj adresy a users v config.dev.json.'
printf '\nPĹ™ipraveno. Ve Windows prohlĂ­ĹľeÄŤi otevĹ™i:\n  NĂˇvod:  %s/howto\n  SprĂˇva: %s\n  Demo:   %s/demo\n  PAC:    %s/proxy.pac\n\nSprĂˇva pouĹľĂ­vĂˇ admin_password z konfigurace. PĹ™ed otevĹ™enĂ­m HTTPS sprĂˇvy importuj veĹ™ejnou CA. UkonÄŤenĂ­: Ctrl+C. Statistiky zĹŻstanou pouze v RAM.\n' "$admin_url" "$admin_url" "$pac_url" "$pac_url"
wait "$app_pid"
