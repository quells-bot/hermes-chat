package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTestCert writes a self-signed certificate for domain where autocert's
// directory cache looks for it, so no certificate authority is contacted.
func writeTestCert(t *testing.T, dir, domain string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	data := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	data = append(data, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, domain), data, 0o600))
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

// startTLSServe runs serve in autocert mode with a cached self-signed
// certificate for domain. The client trusts only that certificate and dials the
// server whatever host the URL names.
func startTLSServe(t *testing.T, domain string) (addr string, client *http.Client) {
	t.Helper()
	migrated(t)
	cacheDir := t.TempDir()
	cert := writeTestCert(t, cacheDir, domain)
	t.Setenv("HERMES_CHAT_TLS_MODE", "autocert")
	t.Setenv("HERMES_CHAT_TLS_DOMAIN", domain)
	t.Setenv("HERMES_CHAT_TLS_CACHE_DIR", cacheDir)
	cfg, err := loadConfig()
	require.NoError(t, err)

	apiLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	debugLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = serve(ctx, cfg, newLogger(io.Discard, slog.LevelError), listeners{api: apiLn, debug: debugLn})
	}()

	addr = apiLn.Addr().String()
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client = &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: pool},
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	t.Cleanup(func() {
		cancel()
		<-finished
		client.CloseIdleConnections()
		http.DefaultClient.CloseIdleConnections()
	})
	return addr, client
}

func TestServe_AutocertNegotiatesHTTP2(t *testing.T) {
	_, client := startTLSServe(t, "chat.test")
	resp, err := client.Get("https://chat.test/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, resp.ProtoMajor, "got %s", resp.Proto)
}

func TestServe_AutocertRefusesOtherHosts(t *testing.T) {
	addr, _ := startTLSServe(t, "chat.test")
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "other.test", InsecureSkipVerify: true})
	if err == nil {
		conn.Close()
	}
	require.Error(t, err) // the handshake fails; no certificate is requested for a host not configured
}

// The certificate manager asks for a certificate for the configured domain and
// no other, whoever connects.
func TestCertManager_HostPolicyAllowsOnlyTheDomain(t *testing.T) {
	m := certManager("chat.test", t.TempDir())
	require.NotNil(t, m.HostPolicy)
	assert.NoError(t, m.HostPolicy(context.Background(), "chat.test"))
	assert.Error(t, m.HostPolicy(context.Background(), "other.test"))
	assert.Error(t, m.HostPolicy(context.Background(), "sub.chat.test"))
}

func TestServe_AutocertAnswersPlainHTTPWith400(t *testing.T) {
	addr, _ := startTLSServe(t, "chat.test")
	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAPIListener_OffReturnsSuppliedUnchanged(t *testing.T) {
	ln, err := apiListener(Config{TLSMode: "off"}, nil)
	require.NoError(t, err)
	assert.Nil(t, ln)
}

func TestAPIListener_AutocertReportsAddressInUse(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer taken.Close()
	_, err = apiListener(Config{TLSMode: "autocert", TLSDomain: "chat.test", DBPath: filepath.Join(t.TempDir(), "x.db"), HTTPAddr: taken.Addr().String()}, nil)
	require.Error(t, err)
}
