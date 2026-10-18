package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"github.com/lqqyt2423/go-mitmproxy/cert"
	"github.com/lqqyt2423/go-mitmproxy/proxy"
	log "github.com/sirupsen/logrus"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"pauseproxy/internal/config"
	"pauseproxy/internal/httpapi"
	"pauseproxy/internal/mitm"
	"pauseproxy/internal/sites"
	"pauseproxy/internal/state"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "PauseProxy:", e)
		os.Exit(1)
	}
}
func run() error {
	command := "run"
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "bootstrap-ca" {
		command = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("pauseproxy", flag.ContinueOnError)
	path := fs.String("config", "", "read-only JSON config (optional)")
	caDir := fs.String("ca-dir", "", "override CA directory")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage:
  pauseproxy [options]
  pauseproxy bootstrap-ca [options]

Commands:
  (default)     Run the proxy using the existing CA.
  bootstrap-ca  Create a CA once, before the first start. Refuses to overwrite
                an existing CA directory. Do not repeat on upgrades.

First setup on OpenWrt:
  pauseproxy bootstrap-ca --config /etc/pauseproxy/config.json
  pauseproxy --config /etc/pauseproxy/config.json

Import only ca-cert.pem from ca_dir into your browser's trusted authorities.
Keep ca-key.pem private. Preserve both files and ca_dir across upgrades so
clients keep trusting the same CA. Restore an existing CA instead of generating
a new one if you already imported its certificate.

Options:
`)
		fs.PrintDefaults()
	}
	if e := fs.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unknown arguments")
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	if *caDir != "" {
		c.CADir = *caDir
	}
	if command == "bootstrap-ca" {
		if e = mitm.Bootstrap(c.CADir); e != nil {
			return e
		}
		fmt.Println("CA created:", filepath.Join(c.CADir, "ca-cert.pem"))
		return nil
	}
	if e = c.ValidateRun(); e != nil {
		return e
	}
	ca, e := mitm.LoadCA(c.CADir)
	if e != nil {
		return e
	}
	public, e := os.ReadFile(filepath.Join(c.CADir, "ca-cert.pem"))
	if e != nil {
		return e
	}
	rules, e := sites.Load(c.SitesFile, c)
	if e != nil {
		return fmt.Errorf("pravidla %s: %w", c.SitesFile, e)
	}
	store := state.New(c, time.Now)
	store.ConfigureRules(rules, c.SitesFile)
	log.SetLevel(log.ErrorLevel) // No access logging; no file output.
	a := &httpapi.API{Config: c, State: store, PublicCert: public}
	upstream, e := mitm.NewUpstreamTransport()
	if e != nil {
		return e
	}
	defer upstream.CloseIdleConnections()
	p, e := proxy.NewProxy(&proxy.Options{Addr: c.ProxyAddr, StreamLargeBodies: 1024 * 1024, UpstreamTransport: upstream, NewCaFunc: func() (cert.CA, error) { return ca, nil }})
	if e != nil {
		return e
	}
	p.SetShouldInterceptRule(func(r *http.Request) bool { return a.Target(r.Host) })
	p.SetUpstreamProxy(func(*http.Request) (*url.URL, error) { return nil, nil })
	p.SetAuthProxy(func(w http.ResponseWriter, r *http.Request) (bool, error) {
		u, _ := c.User(r.RemoteAddr)
		if u == "" {
			return false, fmt.Errorf("unknown device")
		}
		return true, nil
	})
	p.AddAddon(&mitm.Addon{API: a})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 3)
	servers := []*http.Server{{Addr: c.PACAddr, Handler: http.HandlerFunc(a.Public), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}, {Addr: c.AdminAddr, Handler: http.HandlerFunc(a.Admin), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}}
	listeners := []net.Listener{}
	for _, s := range servers {
		l, err := net.Listen("tcp", s.Addr)
		if err != nil {
			for _, old := range listeners {
				_ = old.Close()
			}
			return err
		}
		listeners = append(listeners, l)
	}
	adminURL, _ := url.Parse(c.AdminURL)
	if adminURL.Scheme == "https" {
		adminCert, e := ca.GetCert(adminURL.Hostname())
		if e != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return e
		}
		listeners[1] = tls.NewListener(listeners[1], &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*adminCert}})
	}
	for i, s := range servers {
		go func(s *http.Server, l net.Listener) { errs <- s.Serve(l) }(s, listeners[i])
	}
	go func() { errs <- p.Start() }()
	fmt.Printf("PauseProxy - RAM-only statistics\nAdmin: %s\nPAC: http://%s/proxy.pac\nDemo: http://%s/demo\nProxy: %s\n", c.AdminURL, c.PACAddr, c.PACAddr, c.ProxyAddr)
	select {
	case <-ctx.Done():
	case e = <-errs:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
	_ = p.Close()
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
