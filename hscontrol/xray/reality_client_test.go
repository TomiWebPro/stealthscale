// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealityUClient_ParamValidation(t *testing.T) {
	t.Parallel()

	_, err := RealityUClient(nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil config")

	_, err = RealityUClient(nil, &RealityClientConfig{PublicKey: []byte{1, 2, 3}}, nil) //nolint:exhaustruct_v5 // test fixture: only the fields under test
	require.Error(t, err)
	assert.Contains(t, err.Error(), "publicKey must be 32")
}

func TestVerifyPeerCertificate_Empty(t *testing.T) {
	t.Parallel()

	// No utls connection wired: VerifyPeerCertificate must
	// fail closed instead of panicking on the nil Conn.
	c := &realityUConn{ //nolint:exhaustruct_v5 // test fixture: only the fields under test
		cfg:        &RealityClientConfig{}, //nolint:exhaustruct_v5 // test fixture: only the fields under test
		serverName: "example.com",
		authKey:    nil,
	}

	err := c.VerifyPeerCertificate(nil, nil)
	assert.Error(t, err)
}

// realityTempCert generates a self-signed ed25519 certificate
// whose signature field carries the Reality signature
// HMAC-SHA512(pub, authKey) instead of an ed25519 signature —
// the exact wire shape of the temporary certificate the Reality
// server presents. x509.CreateCertificate cannot produce that
// shape directly (Go's signTBS verifies the signer's output, so
// a custom signer cannot forge an arbitrary signature), so a
// real certificate is created first and its DER is split into
// TBS/algorithm/signatureValue and re-marshalled with the
// Reality signature spliced in. Returns the DER and the public
// key the signature was computed over.
func realityTempCert(t *testing.T, authKey []byte) ([]byte, ed25519.PublicKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{ //nolint:exhaustruct_v5 // test fixture: only the fields under test
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "reality-temp"}, //nolint:exhaustruct_v5 // test fixture: only the fields under test
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	realDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	require.NoError(t, err)

	var cert struct {
		TBS   asn1.RawValue
		AlgID pkix.AlgorithmIdentifier
		Sig   asn1.BitString
	}

	_, err = asn1.Unmarshal(realDER, &cert)
	require.NoError(t, err)

	h := hmac.New(sha512.New, authKey)
	h.Write(pub)

	spliced, err := asn1.Marshal(struct {
		TBS   asn1.RawValue
		AlgID pkix.AlgorithmIdentifier
		Sig   asn1.BitString
	}{
		cert.TBS,
		cert.AlgID,
		asn1.BitString{Bytes: h.Sum(nil), BitLength: sha512.Size * 8},
	})
	require.NoError(t, err)

	return spliced, pub
}

// newRealityUConn wires a realityUConn around a real (idle)
// utls connection, the way RealityUClient does before the
// handshake. VerifyPeerCertificate runs while the handshake
// holds the Conn mutex, so the tests must not call
// ConnectionState (see the note on VerifyPeerCertificate); the
// rawCerts fast path is exercised directly instead.
func newRealityUConn(t *testing.T, serverName string, authKey []byte) *realityUConn {
	t.Helper()

	server, client := net.Pipe()

	t.Cleanup(func() {
		server.Close()
		client.Close()
	})

	uConn := utls.UClient(
		client,
		&utls.Config{ServerName: serverName}, //nolint:exhaustruct_v5 // test fixture: only the fields under test
		utls.HelloChrome_Auto,
	)

	return &realityUConn{ //nolint:exhaustruct_v5 // test fixture: only the fields under test
		UConn:      uConn,
		cfg:        &RealityClientConfig{ServerName: serverName}, //nolint:exhaustruct_v5 // test fixture: only the fields under test
		serverName: serverName,
		authKey:    authKey,
	}
}

// TestVerifyPeerCertificate_AcceptsRealityTempCert pins the
// success path of the anti-stolen-key control: the temporary
// ed25519 certificate the Reality server presents (signature =
// HMAC-SHA512(pub, authKey)) verifies and arms the verified
// flag that RealityUClient gates the handshake on.
func TestVerifyPeerCertificate_AcceptsRealityTempCert(t *testing.T) {
	t.Parallel()

	authKey := make([]byte, 32)
	_, err := rand.Read(authKey)
	require.NoError(t, err)

	der, _ := realityTempCert(t, authKey)

	rc := newRealityUConn(t, "reality.test", authKey)

	require.NoError(t, rc.VerifyPeerCertificate([][]byte{der}, nil))
	assert.True(t, rc.verified, "the Reality temporary certificate must verify")
}

// TestVerifyPeerCertificate_RejectsForeignAuthKey fails closed
// on a certificate signed under a different authKey: a rogue
// server replaying another session's temporary certificate, or
// an attacker holding the server's X25519 public key but not
// this session's authKey, must not arm the verified flag.
func TestVerifyPeerCertificate_RejectsForeignAuthKey(t *testing.T) {
	t.Parallel()

	authKey := make([]byte, 32)
	_, err := rand.Read(authKey)
	require.NoError(t, err)

	der, _ := realityTempCert(t, authKey)

	otherKey := make([]byte, 32)
	_, err = rand.Read(otherKey)
	require.NoError(t, err)

	rc := newRealityUConn(t, "reality.test", otherKey)

	err = rc.VerifyPeerCertificate([][]byte{der}, nil)
	require.Error(t, err, "a certificate signed under a foreign authKey must fail closed")
	assert.False(t, rc.verified, "the verified flag must stay disarmed")
}

// TestVerifyPeerCertificate_RejectsPlainCertificate pins the
// fail-closed posture against an ordinary self-signed
// certificate (a probe, a misrouted decoy, or a rogue server):
// it is not an ed25519 Reality temporary certificate and it
// does not chain to the system roots, so verification fails.
func TestVerifyPeerCertificate_RejectsPlainCertificate(t *testing.T) {
	t.Parallel()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{ //nolint:exhaustruct_v5 // test fixture: only the fields under test
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "probe.example"}, //nolint:exhaustruct_v5 // test fixture: only the fields under test
		DNSNames:     []string{"probe.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)

	rc := newRealityUConn(t, "probe.example", nil)

	err = rc.VerifyPeerCertificate([][]byte{der}, nil)
	require.Error(t, err, "an ordinary self-signed certificate must fail closed")
	assert.False(t, rc.verified)
}

// TestVerifyPeerCertificate_RejectsMissingAuthKey pins that an
// ed25519 certificate cannot verify before the session's authKey
// exists: without the authKey there is no HMAC to match, and the
// self-signed certificate does not chain to the system roots.
func TestVerifyPeerCertificate_RejectsMissingAuthKey(t *testing.T) {
	t.Parallel()

	der, _ := realityTempCert(t, make([]byte, 32))

	rc := newRealityUConn(t, "reality.test", nil)

	err := rc.VerifyPeerCertificate([][]byte{der}, nil)
	require.Error(t, err, "an ed25519 certificate must not verify without the session authKey")
	assert.False(t, rc.verified)
}
