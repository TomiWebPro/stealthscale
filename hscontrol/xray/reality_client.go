// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause
//
// This file is a lightweight Go port of the Reality client from
//   github.com/XTLS/Xray-core/transport/internet/reality/reality.go
//     (Copyright XTLS, Mozilla Public License 2.0)
// and uses
//   github.com/xtls/reality (Copyright (c) 2023 RPRX, MPL-2.0) for the server
//   and github.com/refraction-networking/utls (BSD-3-Clause) for ClientHello.
// See LICENSE (Third-Party Licenses) for full attribution.
// The ported logic (SessionId/AEAD, VerifyPeerCertificate) retains the
// original MPL-2.0 semantics; this file itself is offered under BSD-3-Clause
// as a Larger Work per MPL-2.0 §3.3.
//
// The client proves knowledge of the server's X25519 public key without
// presenting a certificate: it derives a shared AuthKey, embeds version+time+
// shortId in the ClientHello SessionId, and seals it with an AEAD keyed by
// the shared secret. The server validates it and presents a temporary
// ed25519 certificate whose signature is HMAC-SHA512(pub, AuthKey). The
// client's VerifyPeerCertificate checks that HMAC. If verification fails the
// connection is treated as a probe and fallen back to the decoy dest.

package xray

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"time"
	"unsafe"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/crypto/hkdf"
)

// RealityClientConfig holds the client-side Reality parameters.
// PublicKey is the server's X25519 public key (32 bytes). ShortId is the
// short id negotiated with the server (0-8 bytes, may be empty).
type RealityClientConfig struct {
	Show        bool
	Fingerprint string // chrome, firefox, safari, ios, randomized, ...
	ServerName  string // SNI to present; if empty derived from dial addr
	PublicKey   []byte
	ShortId     []byte
	SpiderX     string
}

// realityUConn wraps utls.UConn with Reality auth state.
type realityUConn struct {
	*utls.UConn
	cfg        *RealityClientConfig
	serverName string
	authKey    []byte
	verified   bool
}

// VerifyPeerCertificate is installed as utls.Config.VerifyPeerCertificate.
// It checks the server's temporary ed25519 cert signature == HMAC-SHA512(pub, authKey).
// When Show is false, no debug prints. On success sets verified=true.
func (c *realityUConn) VerifyPeerCertificate(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	p, _ := reflect.TypeOf(c.Conn).Elem().FieldByName("peerCertificates")
	certs := *(*([]*x509.Certificate))(unsafe.Pointer(uintptr(unsafe.Pointer(c.Conn)) + p.Offset))
	if len(certs) == 0 {
		return fmt.Errorf("reality: no peer certificates")
	}
	if pub, ok := certs[0].PublicKey.(ed25519.PublicKey); ok && len(c.authKey) > 0 {
		h := hmac.New(sha512.New, c.authKey)
		h.Write(pub)
		if bytes.Equal(h.Sum(nil), certs[0].Signature) {
			c.verified = true
			return nil
		}
	}
	// Fallback: normal PKI verification against system roots + SNI
	opts := x509.VerifyOptions{DNSName: c.serverName, Intermediates: x509.NewCertPool()}
	for _, cert := range certs[1:] {
		opts.Intermediates.AddCert(cert)
	}
	if _, err := certs[0].Verify(opts); err != nil {
		return err
	}
	return nil
}

func newAesGcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func randInt64(from, to int64) int64 {
	if from >= to {
		return from
	}
	bi, _ := rand.Int(rand.Reader, big.NewInt(to-from))
	return from + bi.Int64()
}

// RealityUClient dials Reality over an existing net.Conn with the given config.
// It performs the Reality ClientHello SessionId trick and verifies the server.
// On success returns a utls-backed Conn ready for VLESS. If verification fails
// (probe or wrong key) it returns an error after a small random delay to avoid
// giving a timing oracle.
func RealityUClient(conn net.Conn, cfg *RealityClientConfig, ctx context.Context) (net.Conn, error) {
	if cfg == nil {
		return nil, fmt.Errorf("reality: nil config")
	}
	if len(cfg.PublicKey) != 32 {
		return nil, fmt.Errorf("reality: publicKey must be 32 bytes, got %d", len(cfg.PublicKey))
	}
	uConn := &realityUConn{cfg: cfg}
	utlsCfg := &utls.Config{
		VerifyPeerCertificate:  uConn.VerifyPeerCertificate,
		ServerName:             cfg.ServerName,
		InsecureSkipVerify:     true,
		SessionTicketsDisabled: false,
		ClientSessionCache:     utls.NewLRUClientSessionCache(32),
	}
	uConn.serverName = utlsCfg.ServerName
	helloID := fpToClientHelloID(cfg.Fingerprint)
	uConn.UConn = utls.UClient(conn, utlsCfg, helloID)

	if err := uConn.BuildHandshakeState(); err != nil {
		return nil, fmt.Errorf("reality: BuildHandshakeState: %w", err)
	}
	hello := uConn.HandshakeState.Hello
	// Overwrite SessionId at its position in Raw. Xray hardcodes 39, but
	// HelloGolang and other fingerprints have different layouts, so locate it
	// dynamically. Fall back to 39 for the common Chrome/Firefox case.
	origSessionId := hello.SessionId
	if len(origSessionId) != 32 {
		origSessionId = make([]byte, 32)
		copy(origSessionId, hello.SessionId)
	}
	offset := -1
	if len(hello.Raw) >= 39+32 && len(origSessionId) == 32 {
		if bytes.Equal(hello.Raw[39:39+32], origSessionId) {
			offset = 39
		}
	}
	if offset == -1 {
		offset = bytes.Index(hello.Raw, origSessionId)
		if offset == -1 {
			// As a last resort, assume 39 if Raw is long enough (Chrome path).
			if len(hello.Raw) >= 39+32 {
				offset = 39
			} else {
				return nil, fmt.Errorf("reality: failed to locate SessionId in Raw (len Raw %d, SessionId %d)", len(hello.Raw), len(origSessionId))
			}
		}
	}
	hello.SessionId = make([]byte, 32)
	copy(hello.Raw[offset:], hello.SessionId)
	// Version bytes — server checks Min/MaxClientVer if set, otherwise any passes.
	hello.SessionId[0] = 1
	hello.SessionId[1] = 0
	hello.SessionId[2] = 0
	hello.SessionId[3] = 0
	binary.BigEndian.PutUint32(hello.SessionId[4:], uint32(time.Now().Unix()))
	copy(hello.SessionId[8:], cfg.ShortId)

	pubKey, err := ecdh.X25519().NewPublicKey(cfg.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("reality: invalid publicKey: %w", err)
	}
	ecdhe := uConn.HandshakeState.State13.KeyShareKeys.Ecdhe
	if ecdhe == nil {
		ecdhe = uConn.HandshakeState.State13.KeyShareKeys.MlkemEcdhe
	}
	if ecdhe == nil {
		return nil, fmt.Errorf("reality: fingerprint %q does not support TLS 1.3 (no ECDHE key), cannot do Reality", helloID.Client)
	}
	shared, err := ecdhe.ECDH(pubKey)
	if err != nil || shared == nil {
		return nil, fmt.Errorf("reality: ECDH failed: %w", err)
	}
	uConn.authKey = shared
	if _, err := hkdf.New(sha256.New, uConn.authKey, hello.Random[:20], []byte("REALITY")).Read(uConn.authKey); err != nil {
		return nil, err
	}
	aead, err := newAesGcm(uConn.authKey)
	if err != nil {
		return nil, fmt.Errorf("reality: NewAesGcm: %w", err)
	}
	// AEAD( SessionId[:16], nonce=Random[20:], aad=Raw )
	aead.Seal(hello.SessionId[:0], hello.Random[20:], hello.SessionId[:16], hello.Raw)
	copy(hello.Raw[offset:], hello.SessionId)

	if err := uConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	if !uConn.verified {
		// No valid Reality signature — this looks like a real decoy cert.
		// Sleep a little to mimic a browser spider before failing.
		d := time.Duration(randInt64(50, 350)) * time.Millisecond
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
		return nil, fmt.Errorf("reality: server presented real certificate (not Reality) — verification failed")
	}
	return uConn, nil
}
