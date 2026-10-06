# PauseProxy

Lokální MVP: Go aplikace, selektivní HTTPS MITM, PAC, JavaScript s heartbeat a správa webů. Statistiky a relace jsou pouze v RAM. Pravidla webů se ukládají do `.local/sites.json`. Počáteční X zachová limit **120 sekund**; nový web má výchozích **15 minut**.

Pro práci na kódu použijte [vývojářský průvodce DEV_README.md](DEV_README.md): architektura, invarianty PAC/MITM, testy, CSP a známé problémy.

## Lokální konfigurace

Před prvním spuštěním zkopírujte `config.dev.json` do `config.local.json` a v kopii nastavte vlastní `admin_password`. `config.local.json` je ignorovaný Gitem a vývojové skripty jej automaticky upřednostní. Šablona neobsahuje heslo. CA a pravidla zůstávají v `.local/`.

## Rychlé spuštění z Ubuntu WSL

V Ubuntu terminálu přejděte do složky projektu (Windows disk C: je dostupný pod `/mnt/c/…`) a spusťte:

```sh
bash ./run_test.sh
```

Skript používá Linux Go ≥ 1.26. Pokud chybí, stáhne Go 1.27.1 z oficiálního zdroje a ověří připnutý SHA-256. Linux toolchain uloží do `.tools/go-linux`, odděleně od Windows `.tools/go`. Podporuje amd64 a arm64; nemění systémový PATH ani neprovádí `sudo`.

Provede testy, `go vet` i race detector, sestaví `dist/pauseproxy-linux` a spustí aplikaci s `config.local.json` (pokud existuje; jinak se šablonou `config.dev.json`). Chybějící CA vytvoří jednorázově, existující znovu použije bez přepsání. Pro neúplnou nebo neplatnou CA skončí s chybou. Aplikace běží, dokud nestisknete **Ctrl+C**; skript ukončí pouze svůj proces.

```sh
bash ./run_test.sh --check       # Jen kontroly; bez serveru a bootstrapu CA
bash ./run_test.sh --skip-race   # Výslovné vynechání race detectoru
bash ./run_test.sh --help
```

V Ubuntu potřebujete `curl`, `python3`, `timeout` (coreutils), pro stažení také `tar` a `sha256sum`. Race detector vyžaduje GCC: `sudo apt update && sudo apt install build-essential`. Skript chybějící závislosti oznámí a nic systémově neinstaluje.

Nejprve ukončete případnou běžící Windows verzi ve vlastním terminálu. Obě instance používají porty **9080/9082/9083** a ve WSL mirrored režimu se mohou střetnout. Skript obsazené porty vysvětlí; cizí procesy neukončuje. Ani samotná kontrola `--check` existující server neovlivní.

Po startu otevřete ve **Windows Chrome nebo Edge** [návod krok za krokem](https://127.0.0.1:9083/howto). Je dostupný bez přihlášení, obsahuje demo, import veřejné CA, nastavení PAC, test X, řešení problémů i návrat k původnímu nastavení. Odkaz je také ve správě. Windows přístup k WSL přes localhost popisuje [dokumentace Microsoftu](https://learn.microsoft.com/en-us/windows/wsl/networking).

Pro Chrome na Androidu je v návodu [samostatný postup pro telefon přes Wi-Fi](https://127.0.0.1:9083/howto#android): LAN konfigurace, WSL mirrored networking nebo Windows běh, firewall pro konkrétní telefon, import veřejné CA, Wi-Fi PAC, test měření a návrat k původnímu nastavení.

Skript lze spustit z libovolné pracovní složky; nastavení čte vždy ze složky projektu. Existující Windows CA v `.local/ca` lze využít i ve WSL, takže při nezměněné CA není nutný nový import. Windows proxy a úložiště důvěryhodných certifikátů nastavujete ručně podle návodu.

## OpenWrt / Turris Omnia

Balíček, služba a routerové testy jsou v samostatném repozitáři [PauseProxy-wrt](https://github.com/ja-pa/PauseProxy-wrt). Ten sestavuje tuto aplikaci z připnutého Git submodulu.

## Rychlé spuštění na Windows

V PowerShellu v kořeni projektu:

```powershell
# Pouze pokud nemáte Go >= 1.26. Stáhne ověřený portable Go do .tools.
./scripts/setup.ps1

# Jednorázově vytvoří CA a její privátní klíč v .local/ca.
./scripts/dev.ps1 bootstrap

# Běžný start CA jen čte. Ukončení: Ctrl+C.
./scripts/dev.ps1 start
```

Pokud PowerShell blokuje skripty, lze konkrétní příkaz spustit přes `powershell -ExecutionPolicy Bypass -File .\scripts\dev.ps1 start`. Není nutné měnit systémovou execution policy.

- Správa: <https://127.0.0.1:9083>
- Demo bez PAC a instalace CA: <http://127.0.0.1:9082/demo>
- PAC: <http://127.0.0.1:9082/proxy.pac>
- Proxy: `127.0.0.1:9080`

Do správy vložte **heslo nastavené v `admin_password`**. Zůstává jen v paměti stránky a nepoužívá cookies ani localStorage. Demo čerpá rozpočet vybraného webu (výchozí X): klikněte na tlačítko, otevřete druhou kartu, skryjte obě nebo upravte minuty webu ve správě. Statistiky se obnovují každé dvě sekundy. Reset i restart zachovají uložená pravidla a limity webů; restart vynuluje statistiky.

S již dostupným Go (Windows i Linux):

```sh
go run ./cmd/pauseproxy bootstrap-ca --config ./config.local.json
go run ./cmd/pauseproxy --config ./config.local.json
go test ./...
go vet ./...
go build -o pauseproxy ./cmd/pauseproxy
```

## Trvalá pravidla webů a média mimo proxy

Ve správě přidejte hlavní doménu, přesné cílové domény, denní minuty, režim Denní limit / Vždy blokovat a zapnutí. Domény jednoho webu sdílejí rozpočet; každý uživatel má vlastní spotřebu. Předvolby X a YouTube pouze vyplní návrh a uloží se tlačítkem Uložit. YouTube nabízí `youtube.com`, `www.youtube.com`, `m.youtube.com`, `youtu.be` a výjimky `googlevideo.com`, `ytimg.com`. X nabízí `x.com`, `www.x.com` a výjimku `twimg.com`.

Výjimky Média mimo proxy zahrnují doménu i její subdomény a mají přednost. Konfliktní cílová doména je odmítnuta. Neznámé subdomény nejsou MITM cíle. Video přímo z cílové domény nelze podle hostitele oddělit. Přímé mediální odkazy mohou zůstat dostupné i při blokaci stránky.

`sites_file` je standardně `.local/sites.json`, relativně k adresáři spouštěcího configu (bez --config k pracovní složce). Configy v kořeni projektu sdílejí stejný soubor. Chybějící soubor vznikne prvním uložením; existující má přednost včetně prázdného seznamu. Neplatný soubor zastaví start a nepřepisuje se. Změny se ukládají atomickým nahrazením; chyba zápisu zachová RAM pravidla i PAC. Podporována je jedna běžící instance; ruční editaci načte restart.

Po změně domén je serverový PAC aktuální ihned, ale prohlížeč drží vlastní PAC kontext. Ve Windows vypněte/zapněte instalační skript se stejnou URL, v Android Wi-Fi vraťte Proxy z Žádná na automatickou konfiguraci. Zavřete dotčené karty; po změně výjimek ukončete stará spojení, případně celý prohlížeč. Existující MITM tunel se nemůže změnit na přímý. [Chování PAC v Chromium](https://chromium.googlesource.com/chromium/src.git/+/refs/heads/lkgr/net/docs/proxy.md).

Odebrání či vypnutí pravidla/domény ukončí měření a odstraní překryv. Statistiky se nemažou; znovu přidaný web během stejného běhu zachová dnešní spotřebu. Reset statistik zachová uložená pravidla. Demo umožní zvolit web.

Seznamová API mají limit JSON 64 KiB, ostatní 4 KiB. Limity: 32 webů, 32 přesných domén na web a 128 výjimek. Nová pravidla mají výchozích 900 sekund; POST také může atomicky přidat `bypass_domains` předvolby. PATCH upravuje cílové domény, zapnutí, režim a sekundový limit; hlavní identifikátor nelze přejmenovat.

## Chrome a X

1. Nejprve ověřte demo. Nepotřebuje změny v systému.
2. Ve Windows otevřete `.local/ca/ca-cert.pem` a ručně importujte **veřejný certifikát** do důvěryhodných kořenových autorit aktuálního uživatele. Alternativně explicitně spusťte `certutil -user -addstore Root .local/ca/ca-cert.pem`. Klíč `ca-key.pem` nikam neimportujte ani nesdílejte. Bootstrap omezuje přístup k CA adresáři na aktuálního uživatele pomocí Windows ACL; na Linuxu používá 0700/0600.
3. Nastavení Windows → Síť a internet → Proxy → Používat instalační skript: `http://127.0.0.1:9082/proxy.pac`.
4. Pro test použijte čistý profil Chrome bez dříve registrovaného service workeru X. Alternativně v DevTools → Application → Service Workers odregistrujte existující service worker tlačítkem Unregister. Nové service workery proxy odmítá. Vložený klient odregistruje staré registrace daného originu; již otevřené karty zavřete a web znovu otevřete. Pokud se načítá uložená stránka bez klienta, odregistrujte worker ručně v DevTools → Application → Service Workers → Unregister a použijte Ctrl+Shift+R.
5. Otevřete `https://x.com`. Ověřte odpočet, kliknutí ve správě, varování, překryv v otevřené kartě a 403 při nové navigaci po vyčerpání limitu.
6. Po experimentu vypněte instalační skript proxy. CA lze odebrat v `certmgr.msc` podle názvu **PauseProxy Development CA**. Aplikace systémové nastavení ani trust store sama nemění.

PAC posílá přes proxy pouze přesné zapnuté cílové domény. Výchozí X má `x.com` a `www.x.com`; `api.x.com` ani jiné subdomény se automaticky nezahrnují. `pbs.twimg.com`, `video.twimg.com` a ostatní necílové weby vrací DIRECT. Necílový HTTPS host doručený explicitní proxy zůstane šifrovaným tunelem. Upstream TLS se ověřuje. [Rozhraní go-mitmproxy](https://github.com/lqqyt2423/go-mitmproxy/tree/v1.9.3) je připnuté na v1.9.3.

## Konfigurace a telefon přes LAN

`config.dev.json` se při startu jen načítá. Bez `--config` fungují stejné vestavěné výchozí hodnoty. Každý přidaný web má vlastní rozpočet pro každého uživatele. Náhodné rozpočty nejsou součástí MVP. OpenWrt integraci spravuje PauseProxy-wrt.

Pro LAN vytvořte vlastní kopii konfigurace:

- `proxy_addr`: `0.0.0.0:9080`, `pac_addr`: `0.0.0.0:9082`.
- `advertised_proxy`: skutečná IP PC, např. `192.168.1.50:9080`.
- V `users` přidejte přesnou IP telefonu, např. `"192.168.1.70": "dev"`. Více IP stejného uživatele sdílí rozpočet. Identita se bere ze spojení, klient ji nevolí.
- Lokálně ponechte `admin_addr` jako `127.0.0.1:9083` a `admin_url` jako `https://127.0.0.1:9083`. Pro LAN nastavte listener, veřejnou HTTPS adresu PC a povolené IP v `users`; neznámé IP odmítá i správa.
- Firewall povolte pro 9080 a 9082 pouze na privátní síti a z testovaných IP.
- Na telefonu použijte PAC `http://192.168.1.50:9082/proxy.pac` a ručně důvěřujte veřejné CA. Některé verze Androidu nebo aplikace mají jiné možnosti PAC a důvěry; cílem je Chrome ve Wi-Fi.

Odkazy při bindu na `0.0.0.0` nahraďte skutečnou LAN IP. `hosts` a `daily_limit_seconds` slouží k počátečnímu převzetí pravidel, pokud `sites_file` ještě neexistuje. Aktuální pravidla řídí PAC i MITM. Limit má rozsah 1–86400 sekund. `blocked_hours` přijímá např. `[{"start":"22:00","end":"06:00"}]`; pracuje v `Europe/Prague` a platí pro všechny zapnuté weby.

## Stav, API a disk

Čas je sjednocení aktivních relací uživatele pro konkrétní web; karta musí být viditelná, mít fokus a mít interakci během posledních 60 sekund. Pohyb ukazatele, kliknutí, klávesnice, dotyk nebo posouvání obnoví minutový interval. Po jeho uplynutí se zobrazí „měření pozastaveno“; další interakce nebo návrat do okna měření obnoví. Pouhé přehrávání videa interval neobnovuje. Ztráta fokusu okna a skrytí karty měření zastaví. Současné karty jej nenásobí. Heartbeat chodí každých 5 sekund a obnovuje 10sekundovou platnost. První heartbeat neúčtuje minulost. Skrytí karty odesílá závěrečné hlášení; ztracené hlášení může přidat nejvýše zbývajících 10 sekund. Denní interval se dělí o místní půlnoci. Kliknutí jsou agregované `isTrusted` události mimo rozhraní PauseProxy; opakovaná dávka nepřidá kliknutí znovu.

RAM drží maximálně 256 relací s expirací po 5 minutách, agregace aktuálního a předchozího dne, limity a nejvýše 128 hostitelských certifikátů. Obsah kliknutí, cíle, souřadnice ani historie URL se neukládají. Statistiky, relace ani provozní logy se neukládají. Pouze explicitní uložení pravidel zapisuje soubor sites_file. HTML se zpracovává v RAM s hranicí 4 MiB (komprimované i rozbalené); ostatní odpovědi se streamují. WebSocket a SSE jsou v tomto MVP odmítnuté, protože upstream uchovává jejich historii v paměti.

Upstream požadavky používají sdílený HTTP transport, který při výpadku umí vytvořit nové spojení nezávisle na spojení prohlížeče. Těla do 256 KiB se krátce bufferují pouze v RAM, aby je Go mohl při bezpečném transportním opakování znovu přečíst; větší těla se streamují. Nezavádíme vlastní automatické opakování POST požadavků. Hláška `context canceled` může vzniknout při zrušení načítání prohlížečem; sama o sobě neznamená pád proxy. Opakované chyby při běžném načítání nebo `cannot rewind body after connection loss` vyžadují kontrolu transportu.

Běžný proces CA a spouštěcí konfiguraci pouze čte. Pravidla webů zapisuje při explicitním uložení ve správě; bootstrap CA je samostatný zápis. Statistiky a relace neukládá. Build, Go cache a cache prohlížeče nejsou součástí této záruky.

| Rozhraní | Endpoint | Ochrana |
| --- | --- | --- |
| Origin X / demo | `GET /__pauseproxy/client.js` | Známá IP |
| Origin X / demo | `POST /__pauseproxy/session` | Známá IP, JSON, kontrola Origin |
| Origin X / demo | `POST /__pauseproxy/heartbeat` | Relace svázaná s IP a uživatelem, instance, sekvence |
| Origin X / demo | `GET /__pauseproxy/status` | Známá IP |
| PAC server | `GET /proxy.pac`, `/healthz`, `/ca-cert.pem`, `/demo` | Známá IP, pouze veřejná CA |
| Správa | `GET /api/state` | HTTPS, povolená IP, přesný Host/Origin, heslo v Bearer hlavičce |
| Správa | `GET/POST /api/sites`, `PATCH/DELETE /api/sites/{domain}` | Token, Host/Origin a JSON |
| Správa | `GET/PUT /api/bypass-domains` | Totéž; média mimo proxy |
| Správa | `PATCH /api/limits` | 410: použijte editor webů |
| Správa | `POST /api/reset` | Totéž + JSON `{}` |

Všechna stavová API mají no-store, limit JSON těla 4 KiB (seznamová API pravidel 64 KiB) a mutace nemají CORS. ID relace je náhodný token; po resetu/restartu si klient vytvoří novou a zahodí staré neodeslané dávky. Ztráta klientského API po 10 sekundách vyvolá překryv. Zvýšení limitu obnoví otevřenou kartu při dalším heartbeat; na blokovací stránce použijte obnovení.

## Ověření

```powershell
./scripts/dev.ps1 test
./.tools/go/bin/go.exe vet ./...
# Race detector na Windows potřebuje C compiler; portable varianta:
./scripts/setup.ps1 -WithRaceCompiler
./scripts/dev.ps1 race
./scripts/dev.ps1 build
./dist/pauseproxy.exe --config ./config.local.json
```

Na Linuxu s GCC: `go test -race ./...`.

Automatické testy pokrývají souběh, skrytí, expiraci, změny limitů, půlnoc, reset, deduplikaci, IP vazbu, kapacitu, ochranu API, whitelist, kompresi/CSP, chybovou injekci a HTTPS CONNECT s důvěrou k testovací CA. `go vet` kontroluje zdroje.

Ruční kontrola pro skutečný Chrome/X/telefon: SPA navigace, návrat Zpět, cache/service worker, uspání telefonu, přímá cesta médií a chování při výpadku. Pro audit zápisů použijte Process Monitor s filtrem na `pauseproxy.exe`, spusťte binárku po bootstrapu a sledujte operace WriteFile. Tento audit není nahrazen testem CA souborů.

MVP nepředstavuje ochranu proti vědomému obejití: PAC lze vypnout a skript sdílí origin s X. Aktuální X, jeho CSP, domény API i Android vyžadují ruční ověření; proxy nezaručuje blokaci již cachované stránky bez vloženého klienta. OpenWrt balíček spravuje PauseProxy-wrt; trvalé statistiky nejsou implementované.

Klient se vkládá přímo na začátek HTML head s CSP nonce, před skripty webu. DOM sestavuje bez `innerHTML`, takže nevyžaduje výjimku pro Trusted Types. Odesílá heartbeat každých 5 sekund a účtuje pouze viditelnou kartu s fokusem a aktivitou během poslední minuty. Správa ukazuje RAM diagnostiku injekcí, vytvořených relací a přijatých heartbeatů za běh procesu (reset statistik ji nenuluje). Po aktualizaci restartujte aplikaci a obnovte X přes Ctrl+Shift+R. V konzoli má být `[PauseProxy] Klient spuštěn`; pokud není a injekce nepřibývají, ověřte PAC a cache/service worker v čistém profilu.

Testy klientské aktivity lze spustit s dostupným Node.js: `node --test web/client_test.cjs`. Kontrolují minutovou nečinnost, skutečné versus syntetické interakce, fokus, skrytí, návrat z historie, blokaci a kliknutí pomocí řízených hodin bez minutového čekání.

## Nové přihlášení a HTTPS správa

Před spuštěním vyplňte neprázdné `admin_password` v konfiguraci. Prázdná hodnota v příkladu je úmyslná: žádné sdílené výchozí heslo neexistuje. Skutečné heslo necommitujte. `bootstrap-ca` heslo nepotřebuje.

Před prvním otevřením správy importujte veřejnou CA z `ca_dir/ca-cert.pem` nebo ji stáhněte přes `http://127.0.0.1:9082/ca-cert.pem`. HTTPS správa používá stejnou CA. Heslo se drží jen v paměti stránky; reload nebo odhlášení vyžaduje nové přihlášení.

Pro telefon nastavte například `admin_addr: "0.0.0.0:9083"` a `admin_url: "https://192.168.1.50:9083"` s vlastní LAN IP počítače. Do `users` přidejte IP telefonu i PC. Na obou zařízeních používejte přesnou `admin_url`. Firewall povolte pro porty 9080, 9082 a 9083 pouze potřebným zařízením. Úplný příklad a postup instalace CA obsahuje webový návod `/howto#android`.

Správa má taby Overview, Sites & limits, Media exceptions a Connection. Odpočet má formát `m:ss` / `h:mm:ss`; samostatné ozubené kolo otevírá `admin_url/#sites`.
