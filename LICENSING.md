# Licensing

PauseProxy's original code and documentation are licensed under MIT; see
[LICENSE](LICENSE). Copyright holder: ja-pa, 2026. Third-party code remains
under its original terms. The MIT declaration does not replace those licenses.

MIT fits this project because it permits use, modification and redistribution,
including commercial use, with a short attribution requirement. The dependencies
reviewed below do not require licensing PauseProxy's own code under copyleft.
Apache-2.0 would also be a possible choice, with an explicit patent grant, but
is not required merely because the application uses an Apache-licensed library.

## Dependency review (2026-10-18)

Reviewed `go.mod`, the actual Linux `go list -deps ./cmd/pauseproxy` graph,
module license files (including nested licenses), and bundled web assets.

| Component | Version | License |
| --- | --- | --- |
| github.com/andybalholm/brotli | v1.2.1 | MIT; flate: BSD-3-Clause |
| github.com/lqqyt2423/go-mitmproxy | v1.9.3, local modified copy | MIT |
| github.com/sirupsen/logrus | v1.9.4 | MIT |
| github.com/golang/groupcache | 20210331224755-41bb18bfe9da | Apache-2.0 |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/gorilla/websocket | v1.5.3 | BSD-2-Clause |
| github.com/klauspost/compress | v1.18.6 | BSD-3-Clause; bundled xxhash: MIT |
| golang.org/x/net | v0.55.0 | BSD-3-Clause, additional patent grant |
| golang.org/x/sys | v0.45.0 | BSD-3-Clause, additional patent grant |
| golang.org/x/text | v0.37.0 | BSD-3-Clause, additional patent grant |
| Go runtime | Go 1.27.1 used for review | BSD-3-Clause, additional patent grant |
| MVP.css | v1.18 | MIT |

The compress module also contains separately licensed packages (including
Apache-2.0 gzhttp) that are not imported by this executable. Their license
files are retained in the notice bundle as a conservative module-wide inventory.
Likewise, dependencies used only by upstream module tests or tools are not
automatically part of the PauseProxy executable.

## Distribution

Distribute [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt)
alongside binary releases. Preserve the existing licenses in
`third_party/go-mitmproxy/LICENSE` and `web/MVP-LICENSE.txt` in source releases.
The locally modified go-mitmproxy remains MIT licensed; its original copyright
notice is preserved. Keep original file-level attribution notices as well.

When dependencies, build tags, target platform or Go version change, repeat
the review and refresh the notice bundle from the exact module/toolchain files.
It includes full LICENSE/COPYING/NOTICE/PATENTS files, not just SPDX labels.
No separate NOTICE file was present in the reviewed dependency modules.

OpenWrt integration is maintained in PauseProxy-wrt. Its pinned older source
receives the same MIT declaration through a licensing-only patch until a newer
licensed source release is selected; this does not upgrade application code.

References: [MIT](https://choosealicense.com/licenses/mit/),
[Apache-2.0](https://www.apache.org/licenses/LICENSE-2.0), and the exact license
texts preserved in THIRD_PARTY_NOTICES.txt.
