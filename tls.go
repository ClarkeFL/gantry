package main

// Self-signed HTTPS on the bare IP: one port serves both protocols, the
// first byte of each connection says whether it's a TLS handshake (0x16) or
// plain HTTP, which gets a redirect to https://. The cert is generated once
// into the data dir and regenerated if the server's IP is no longer in it.

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func certPaths() (string, string) {
	return filepath.Join(dataDir, "tls.crt"), filepath.Join(dataDir, "tls.key")
}

func loadOrCreateCert() (tls.Certificate, error) {
	crtPath, keyPath := certPaths()
	ip := serverIP()
	if cert, err := tls.LoadX509KeyPair(crtPath, keyPath); err == nil {
		if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
			for _, san := range leaf.IPAddresses {
				if san.String() == ip || ip == "" {
					return cert, nil
				}
			}
		}
		// server IP changed since the cert was made, fall through and regenerate
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "gantry"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	if p := net.ParseIP(ip); p != nil {
		tmpl.IPAddresses = append(tmpl.IPAddresses, p)
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	os.MkdirAll(dataDir, 0o755)
	os.WriteFile(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return tls.LoadX509KeyPair(crtPath, keyPath)
}

// peekedConn replays the byte(s) already read for protocol sniffing.
type peekedConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.br.Read(p) }

// chanListener turns a channel of conns into a net.Listener.
type chanListener struct {
	ch   chan net.Conn
	addr net.Addr
}

func (l *chanListener) Accept() (net.Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}
func (l *chanListener) Close() error   { return nil }
func (l *chanListener) Addr() net.Addr { return l.addr }

// serveDual serves HTTPS (self-signed) and plain-HTTP-redirect on one port.
func serveDual(addr string, mux http.Handler) error {
	cert, err := loadOrCreateCert()
	if err != nil {
		log.Printf("tls: %v, falling back to plain HTTP", err)
		return http.ListenAndServe(addr, mux)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpLn := &chanListener{make(chan net.Conn), ln.Addr()}
	tlsLn := &chanListener{make(chan net.Conn), ln.Addr()}
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
	go http.Serve(httpLn, redirect)
	go http.Serve(tls.NewListener(tlsLn, &tls.Config{Certificates: []tls.Certificate{cert}}), mux)
	log.Printf("https on (self-signed certificate, your browser will warn once)")
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func(c net.Conn) {
			c.SetReadDeadline(time.Now().Add(10 * time.Second))
			br := bufio.NewReader(c)
			b, err := br.Peek(1)
			if err != nil {
				c.Close()
				return
			}
			c.SetReadDeadline(time.Time{})
			pc := &peekedConn{c, br}
			if b[0] == 0x16 { // TLS handshake record
				tlsLn.ch <- pc
			} else {
				httpLn.ch <- pc
			}
		}(c)
	}
}
