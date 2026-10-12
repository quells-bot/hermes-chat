package main

import (
	"crypto/tls"
	"net"

	"golang.org/x/crypto/acme/autocert"
)

// certManager obtains and renews a certificate for one domain, caching it on disk.
func certManager(domain, cacheDir string) *autocert.Manager {
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache(cacheDir),
	}
}

// apiListener returns the listener the API server uses. With TLS off it is
// supplied unchanged, and nil means the server listens on its own address.
// With autocert it is wrapped in TLS, which also answers the TLS-ALPN
// challenge on the same port and negotiates HTTP/2.
func apiListener(cfg Config, supplied net.Listener) (net.Listener, error) {
	if cfg.TLSMode == "off" {
		return supplied, nil
	}
	ln := supplied
	if ln == nil {
		var err error
		if ln, err = net.Listen("tcp", cfg.HTTPAddr); err != nil {
			return nil, err
		}
	}
	return tls.NewListener(ln, certManager(cfg.TLSDomain, cfg.tlsCacheDir()).TLSConfig()), nil
}
