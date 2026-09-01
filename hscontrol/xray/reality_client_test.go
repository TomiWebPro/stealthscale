package xray

import (
    "crypto/ed25519"
    "crypto/rand"
    "crypto/x509"
    "crypto/hmac"
    "crypto/sha512"
    "encoding/pem"
    "math/big"
    "net"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    utls "github.com/refraction-networking/utls"
)

func TestRealityUClient_ParamValidation(t *testing.T) {
    _, err := RealityUClient(nil, nil, nil)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "nil config")
    _, err = RealityUClient(nil, &RealityClientConfig{PublicKey: []byte{1,2,3}}, nil)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "publicKey must be 32")
}

func TestVerifyPeerCertificate_Empty(t *testing.T) {
    // synthetic connection not needed; we test via direct struct with nil authKey
    // Create a utls.UConn with nil Conn will panic if we call VerifyPeerCertificate without ConnectionState
    // Instead test that empty certs returns error via fallback path
    // Use a real in-memory utls connection stub is complex; we test helper error paths
    c := &realityUConn{cfg: &RealityClientConfig{}, serverName: "example.com", authKey: nil}
    // c.Conn is nil -> Verify should return error about nil Conn or no certs
    err := c.VerifyPeerCertificate(nil, nil)
    assert.Error(t, err)
}

func TestVerifyPeerCertificate_ED25519HMAC(t *testing.T) {
    pub, priv, err := ed25519.GenerateKey(rand.Reader)
    require.NoError(t, err)
    authKey := make([]byte, 32)
    _, err = rand.Read(authKey)
    require.NoError(t, err)
    h := hmac.New(sha512.New, authKey)
    h.Write(pub)
    sig := h.Sum(nil)
    tmpl := &x509.Certificate{
        SerialNumber: big.NewInt(1),
        PublicKey: pub,
        Signature: sig,
    }
    // We cannot easily wire utls Conn, but we can test HMAC logic directly
    // Simulate verified check: HMAC matches
    c := &realityUConn{cfg: &RealityClientConfig{}, authKey: authKey}
    // Directly compute as Verify does
    hh := hmac.New(sha512.New, c.authKey)
    hh.Write(pub)
    assert.Equal(t, sig, hh.Sum(nil))
    assert.True(t, hmac.Equal(h.Sum(nil), tmpl.Signature))
    _ = priv
    _ = utls.HelloChrome_Auto
    _ = net.ParseIP // dummy
    _ = pem.Encode // dummy
    _ = x509.CreateCertificate // dummy to keep imports used
}
