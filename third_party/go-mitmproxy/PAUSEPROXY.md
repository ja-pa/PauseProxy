# PauseProxy transport hook

Runtime source subset of github.com/lqqyt2423/go-mitmproxy v1.9.3 (proxy,
cert and internal packages), with its original LICENSE and module files.
The upstream version and checksum remain pinned in the root go.mod/go.sum.

Local changes: `proxy.Options.UpstreamTransport` optionally replaces the
shared client's transport in `newAttacker`. All other runtime source is
unchanged. This exported hook lets PauseProxy configure timeouts and HTTP/2
health checks without reflection, changes to the module cache, or a second
implementation of request forwarding. If the option is nil, upstream
behavior is preserved. Remove this replacement when upstream offers the hook.
