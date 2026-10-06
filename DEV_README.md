# PauseProxy — vývojářský průvodce

Tento dokument popisuje současnou implementaci, její invarianty, testování a známé problémy. Uživatelské spuštění a nastavení prohlížeče jsou v [README.md](README.md) a na [stránce howto](https://127.0.0.1:9083/howto).

## Lokální vývoj

Pro první start zkopíruj `config.dev.json` do ignorovaného `config.local.json` a nastav v kopii heslo.

Vyžaduje Go ≥ 1.26. Vývojové skripty mohou připravit připnuté Go 1.27.1. Windows a Linux mají oddělené toolchainy; CA a pravidla mohou sdílet.

### Ubuntu WSL

Z kořene projektu:

```sh
bash ./run_test.sh --check       # go test, go vet, go test -race
bash ./run_test.sh               # kontroly, build, případný bootstrap CA, start
node --test web/client_test.cjs  # zvlášť; run_test.sh testy JavaScriptu nespouští
```

Race detector potřebuje GCC (`build-essential`). `--skip-race` je výslovné vynechání kontroly, nikoli plná validace. `--check` nespouští server ani nevytváří CA. Skript lze volat i z jiné pracovní složky.

Build IPK, procd a routerové testy jsou v [PauseProxy-wrt](https://github.com/ja-pa/PauseProxy-wrt). Aplikační změny proveď zde, commitni je a ve WRT repozitáři aktualizuj připnutý submodul.

Při použití vlastního configu, po přípravě CA:

```sh
.tools/go-linux/go/bin/go build -o dist/pauseproxy-linux ./cmd/pauseproxy
./dist/pauseproxy-linux --config ./config.android.json
```

Pokud skript použil systémové Go a lokální toolchain nestáhl, nahraď cestu příkazem `go`. Bez `--check` skript upřednostní ignorovaný `config.local.json`, jinak použije šablonu `config.dev.json`.

### Windows PowerShell

```powershell
./scripts/setup.ps1                  # pokud chybí vhodné Go
./scripts/dev.ps1 bootstrap          # jen pro dosud nevytvořenou CA
./scripts/dev.ps1 test
./.tools/go/bin/go.exe vet ./...
./scripts/setup.ps1 -WithRaceCompiler # pokud chybí compiler pro race detector
./scripts/dev.ps1 race
node --test web/client_test.cjs
./scripts/dev.ps1 start
```

Build: `./scripts/dev.ps1 build`. Vlastní konfigurace: `./scripts/dev.ps1 start -Config config.android.json`. Pro `vet` použij systémové `go`, pokud lokální toolchain neexistuje.

Soubory v `web/` jsou vložené do binárky přes `go:embed`. Po jejich změně znovu sestav/restartuj aplikaci a obnov stránku v prohlížeči. Žádný frontend bundler ani hot reload zde není.

## Mapa projektu

| Umístění | Odpovědnost |
| --- | --- |
| `cmd/pauseproxy/main.go` | Konfigurace, CA, načtení pravidel, sestavení proxy a HTTP serverů, ukončení |
| `internal/config/` | Výchozí spouštěcí konfigurace, IP → uživatel, adresy, časová zóna |
| `internal/sites/` | Formát pravidel, normalizace a validace domén, výběr cíle, atomické ukládání |
| `internal/state/` | RAM relace, denní spotřeba, sjednocení karet, změny pravidel za běhu |
| `internal/httpapi/` | Klientské a admin API, PAC, veřejný certifikát, demo a howto |
| `internal/mitm/` | CA, blokace požadavků, streamování, HTML injekce a úprava CSP |
| `web/` | Vložené HTML/CSS/JS a testy klienta v Node.js |
| `scripts/`, `run_test.sh` | Příprava toolchainů a vývojové příkazy |

Výchozí porty: proxy **9080**, veřejný server **9082**, správa **9083**. Správa používá HTTPS a `admin_password` z konfigurace; heslo nemá výchozí hodnotu ani se nevypisuje do konzole.

## Tok požadavku a výběr MITM

PAC a rozhodnutí o zachycení HTTPS používají stejné aktuální pravidlo:

1. `bypass_domains`: uvedená doména **i její subdomény** mají přednost, PAC vrací `DIRECT`.
2. Přesná cílová doména zapnutého webu: PAC vrací proxy a nový HTTPS tunel používá MITM.
3. Ostatní hostitelé: PAC vrací `DIRECT`; při ruční explicitní proxy zůstává HTTPS šifrovaným tunelem.

Nezaváděj wildcardy ani implicitní zahrnutí všech subdomén do cílových domén. Přidání `youtube.com` nesmí zachytit `googlevideo.com`, `r1.googlevideo.com` ani neznámou subdoménu YouTube. Předvolby jsou návrhy ve správě, nikoli rozpoznávání CDN.

Výjimky se rozhodují podle hostitele CONNECT před MITM. Typ odpovědi `video/*` tuto ochranu nenahrazuje. Video ze stejné cílové domény jako HTML nelze podle domény oddělit. Přímé mediální odkazy mohou fungovat i při zákazu webu.

Změna pravidel aktualizuje serverový PAC ihned. Prohlížeč může používat starý PAC a otevřená spojení. Při síťovém testu obnov PAC a zavři dotčené karty/spojení; existující MITM tunel se neumí změnit zpět na přímý. Postup pro Windows a Android je v howto.

## Co je na disku a co v RAM

- `config.dev.json`: při startu čtená konfigurace; `hosts` a původní limit slouží k migraci, pokud soubor pravidel chybí.
- `.local/sites.json`: trvalá pravidla; vznikne při prvním úspěšném uložení. Existující soubor má přednost, i s prázdným seznamem webů.
- `.local/ca/`: veřejný certifikát a privátní klíč. Běžný start je znovu používá. Neplatnou CA automaticky nepřepisuj.
- `.tools/`, `dist/`: vývojové toolchainy, pomocné soubory a sestavené binárky.
- RAM: relace, spotřeba, kliknutí, návštěvy a diagnostické čítače. Restart je zahodí. Reset statistik zachová uložená pravidla.

`sites_file` se řeší vůči adresáři spouštěcí konfigurace, bez `--config` vůči pracovní složce. Kopie konfigurací v kořeni projektu tedy mohou sdílet `.local/sites.json`. Spouštěj jen jednu instanci nad stejným souborem; serializace zápisů platí uvnitř procesu, není to synchronizace více procesů. Ruční editaci pravidel načte restart.

Formát verze 1:

```json
{
  "version": 1,
  "sites": [{
    "domain": "youtube.com",
    "domains": ["youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be"],
    "enabled": true,
    "mode": "limit",
    "daily_limit_seconds": 900
  }],
  "bypass_domains": ["googlevideo.com", "ytimg.com"]
}
```

`domain` je stabilní identifikátor rozpočtu a musí být mezi cílovými doménami. Režim je `limit` nebo `block`; limit má 1–86400 sekund. Formulář zadává minuty a převádí je na sekundy. Nejvýše 32 webů, 32 domén na web a 128 výjimek. Domény se normalizují na malá písmena, bez koncové tečky, s IDN převodem. URL, porty, IP a wildcardy jsou odmítnuté. Přesná doména smí patřit jen jednomu webu a nesmí být pokrytá výjimkou.

Změny pravidel probíhají pod mutexem: kopie → úprava → validace → dočasný soubor a atomické nahrazení → zveřejnění v RAM. Windows používá `MoveFileEx`, Linux `Rename`. Při chybě zápisu musí zůstat původní pravidla i PAC. Neplatný existující soubor musí zastavit start, nikoli se nahradit výchozími pravidly.

## Měření a klient

Relaci vytváří server a váže ji na uživatele, IP, web a původní hostitele. Heartbeat nemůže vybrat jiný web. Rozpočet se počítá podle **uživatele + identifikátoru webu + místního data**. Více IP téhož uživatele sdílí rozpočet webu; souběžné karty jej nenásobí.

Klient hlásí aktivitu každých 5 sekund. Serverová platnost je 10 sekund. Aktivní znamená viditelnou kartu, fokus a interakci během poslední minuty. Samotné přehrávání videa neobnovuje aktivitu. Odchod posílá neaktivní hlášení; při jeho ztrátě doběhne nejvýše zbývající platnost. Souběžné události fokusu se slučují, aby aktivní heartbeat nenarážel na sekundový rate limit serveru.

Sekvence deduplikuje kliknutí. Čas se dělí o místní půlnoci podle konfigurace. Změny limitů a zákaz se projeví na dalším heartbeat i dalších požadavcích. Vypnutí webu či odebrání domény ukončí měření; klient odstraní překryv a přestane hlásit. Odebrání a opětovné přidání stejného identifikátoru v témže běhu nemaže spotřebu. Společná `blocked_hours` platí pro všechny zapnuté weby.

## HTML, CSP a transport

Zpracovává se HTML do 4 MiB, včetně kontroly po dekompresi; podporováno je identity, gzip, deflate a Brotli. Ostatní odpovědi se streamují. Upstream TLS se ověřuje. Malá těla požadavků do 256 KiB mají možnost bezpečného transportního opakování; vlastní plošné opakování POSTů zde není.

Klient se vkládá před skripty webu na začátek `head`. Používá DOM API bez `innerHTML`, takže nevyžaduje odstranění Trusted Types. WebSocket, SSE a nové service workery jsou v současném MVP odmítnuté. Vložený klient při startu odregistruje staré service workery tohoto originu. Již řízené karty mohou používat worker až do zavření; zavři je a web znovu otevři. Uložená stránka bez vloženého klienta cleanup nespustí: použij čistý profil nebo DevTools → Application → Service Workers → Unregister a Ctrl+Shift+R. Cache Storage ani cookies klient nemaže.

Při úpravě CSP zachovej efektivní oprávnění stránky:

- Chybějící fetch direktiva bez `default-src` je neomezená. Nepřidávej jí restriktivní `'self'`.
- Samostatná zásada `require-trusted-types-for 'script'` nesmí získat nová omezení skriptů, stylů či spojení.
- Nonce potlačuje jinak účinné `'unsafe-inline'`. Takovou zásadu ponech, aby původní inline skripty dál fungovaly.
- U restriktivní zásady přidej nonce pro klienta a zachovej původní nonce/hash a zdroje webu. Zkontroluj všechny CSP hlavičky i meta zásady, včetně report-only.

Tyto případy mají regresní testy v `internal/mitm/addon_test.go`. Dřívější porucha YouTube vznikla právě přidáním nových CSP omezení. Neřeš podobnou chybu úplným odstraněním CSP, Trusted Types nebo ověřování TLS.

## API a bezpečnostní hranice

Admin API vyžaduje heslo v Bearer hlavičce a kontrolu Host/Origin. Pravidla: `GET/POST /api/sites`, `PATCH/DELETE /api/sites/{domain}`, `GET/PUT /api/bypass-domains`. Staré `PATCH /api/limits` vrací 410. Seznamová těla mají limit 64 KiB, ostatní 4 KiB; JSON se validuje. `/howto` a `/connection` jsou bez přihlášení za kontrolou přístupu správy; neobsahují heslo ani privátní klíč.

Klientské endpointy jsou pod `/__pauseproxy/` na cílovém originu a demo serveru. Demo může vybrat web přes `?site=youtube.com`. Identita pochází ze spojení a mapování IP v konfiguraci. Pro telefon nastav `admin_addr` na LAN rozhraní nebo wildcard, `admin_url` na HTTPS adresu PC a povol zařízení v `users`; postup je v howto.

MVP není ochrana proti vědomému obejití: uživatel může vypnout PAC a stránka sdílí origin s klientem. Nezapisuj admin heslo, CA klíč, podepsané video URL ani uživatelské síťové výpisy do repozitáře.

## Ověření změn

Podle změny spusť relevantní testy a před dokončením funkční změny Go testy, vet, race detector a klientské testy z úvodu. Testy pokrývají nezávislé rozpočty, sjednocení relací, půlnoc, deduplikaci, změny za běhu, validaci a ukládání pravidel, ochranu API, skutečné vyhodnocení PAC s dostupným Node.js a HTTPS tunely bez certifikátu/injekce PauseProxy pro výjimky.

Pro změny frontendu ověř demo a správu v prohlížeči. Pro změny CSP/transportu zkontroluj skutečný cílový web, konzoli a Network; úspěšný HTTP 200 s vloženým klientem sám nepotvrzuje přehrávání. Skutečné X/YouTube, cache, rozšíření a telefon vyžadují ruční kontrolu.

Pro souběžný test vlastní instance použij samostatný config s jinými třemi porty a samostatným `sites_file`; PAC musí mít odpovídající `advertised_proxy`. Neukončuj cizí procesy. Sdílenou existující CA jen čti; nekopíruj její klíč do testovacích fixture souborů.

## Známé problémy a diagnostika

Stav k 4. 10. 2026; následující body nejsou slibem opravy:

| Příznak | Interpretace / co zjistit |
| --- | --- |
| YouTube funguje, ale `/api/stats/*` a `/ptracking` vrací 502 | Selhávají statistické požadavky. Z konzole nelze určit, zda selhal transport, upstream nebo injekce odpovědi. Hypotéza k ověření: prázdná odpověď označená jako HTML může být chybně předána injekci a změněna na 502. Zjisti původní status, Content-Type, tělo a serverovou chybu. Tento problém zatím není opravený ani potvrzený. |
| `googlevideo.com/videoplayback` vrací 403 | Výjimka má jít přímo. Ověř aktuální PAC a stará spojení; konkrétní odmítnutí může souviset s URL, přehrávačem či rozšířením. Bez odpovědi serveru neurčuj příčinu. |
| `context canceled` | Může být běžné zrušení požadavku při navigaci; četné chyby během stabilního přehrávání vyžadují kontrolu transportu. |
| Chybí `[PauseProxy] Klient spuštěn` | Ověř přesnou cílovou doménu, PAC, cache/service worker, CSP a RAM diagnostiku injekcí ve správě. |
| `about:blank` sandbox, manifest, instalační banner, LegacyDataMixin | Hlášení YouTube/prohlížeče; sama neurčují závadu PauseProxy. Výpisy `xfready…` patří Fready. |

Konzole často násobí výpis dlouhými zásobníky volání. Pro diagnostiku seskupuj podle endpointu, statusu a hlavního sdělení; nepovažuj počet řádků za počet různých chyb. Před sdílením výpisu odstraň tokeny, cookies a podepsané URL. Statistiky PauseProxy nemají sloužit jako síťový archiv.

## Nové přihlášení a HTTPS správa

Před spuštěním vyplňte neprázdné `admin_password` v konfiguraci. Prázdná hodnota v příkladu je úmyslná: žádné sdílené výchozí heslo neexistuje. Skutečné heslo necommitujte. `bootstrap-ca` heslo nepotřebuje.

Před prvním otevřením správy importujte veřejnou CA z `ca_dir/ca-cert.pem` nebo ji stáhněte přes `http://127.0.0.1:9082/ca-cert.pem`. HTTPS správa používá stejnou CA. Heslo se drží jen v paměti stránky; reload nebo odhlášení vyžaduje nové přihlášení.

Pro telefon nastavte například `admin_addr: "0.0.0.0:9083"` a `admin_url: "https://192.168.1.50:9083"` s vlastní LAN IP počítače. Do `users` přidejte IP telefonu i PC. Na obou zařízeních používejte přesnou `admin_url`. Firewall povolte pro porty 9080, 9082 a 9083 pouze potřebným zařízením. Úplný příklad a postup instalace CA obsahuje webový návod `/howto#android`.

Správa má taby Overview, Sites & limits, Media exceptions a Connection. Odpočet má formát `m:ss` / `h:mm:ss`; samostatné ozubené kolo otevírá `admin_url/#sites`.
