// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// VLESS protocol constants.
const (
	// vlessVersion is the only VLESS protocol version we speak.
	vlessVersion = 0

	// vlessUUIDLen is the length of the UUID identifying the user.
	vlessUUIDLen = 16

	// vlessAddonsLenLen is the length of the addons length field.
	vlessAddonsLenLen = 1

	// vlessCommandTCP marks a TCP stream in the VLESS addons.
	vlessCommandTCP uint16 = 0x01

	// vlessUUIDNamespace is the namespace used as a fallback ONLY when no
	// server secret is configured. In production a per-server secret keys
	// the derivation (see NodeUUID), so endpoints are not enumerable by an
	// outsider who knows this public namespace.
	vlessUUIDNamespace = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
)

// hmacSum returns the HMAC-SHA256 of label keyed by secret.
func hmacSum(secret, label string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(label))
	return mac.Sum(nil)
}

// VLESSConfig represents the configuration for a VLESS endpoint.
type VLESSConfig struct {
	ID         string        `json:"id"`                   // UUID for authentication
	Network    string        `json:"network"`              // Network type (tcp, ws, etc.)
	Address    string        `json:"address"`              // Listen address
	Port       int           `json:"port"`                 // Listen port
	Security   string        `json:"security"`             // Security setting (none, tls, xtls)
	Alpn       string        `json:"alpn,omitempty"`       // ALPN for TLS
	Timeout    time.Duration `json:"timeout,omitempty"`    // Connection timeout
	Dest       string        `json:"dest,omitempty"`       // Reality decoy destination (host:port)
	FP         string        `json:"fp,omitempty"`         // uTLS fingerprint to mimic
	PublicKey  string        `json:"pbk,omitempty"`        // Reality public key (hex)
	ShortID    string        `json:"sid,omitempty"`        // Reality short id (hex)
	SpiderX    string        `json:"spider_x,omitempty"`   // Reality spiderX path
}

// NewVLESSConfig creates a default VLESS configuration.
func NewVLESSConfig(id, address string, port int) *VLESSConfig {
	return &VLESSConfig{
		ID:       id,
		Network:  "tcp",
		Address:  address,
		Port:     port,
		Security: "none",
		Timeout:  30 * time.Second,
	}
}

// Validate checks if the VLESS configuration is valid.
func (c *VLESSConfig) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("id is required")
	}
	if c.Address == "" {
		return fmt.Errorf("address is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Port)
	}
	sec := c.Security
	if sec == "reality" {
		sec = "reality_xtls"
	}
	switch sec {
	case "none", "tls", "xtls", "reality_xtls":
		// Valid — reality_xtls is the default stealth mode
	default:
		return fmt.Errorf("invalid security mode: %s", c.Security)
	}
	return nil
}

// URI returns the URI form of the VLESS endpoint, e.g.
// vless://<uuid>@<address>:<port>?security=<security>, suitable for
// distribution to clients.
// For reality_xtls, the URI includes reality parameters so a patched client
// can perform the Reality handshake with uTLS. The decoy destination,
// public key and short id are embedded so the client can validate the
// server and present the correct SNI.
func (c *VLESSConfig) URI() string {
	// Default to reality_xtls for stealth; callers with Security=="none" get legacy
	sec := c.Security
	if sec == "" {
		sec = "reality_xtls"
	}
	if sec == "reality" {
		sec = "reality_xtls"
	}
	base := fmt.Sprintf("vless://%s@%s:%d?security=%s", c.ID, c.Address, c.Port, sec)
	if sec == "reality_xtls" {
		// Append uTLS fingerprint and reality hints for client
		fp := c.FP
		if fp == "" {
			fp = "chrome"
		}
		base += "&fp=" + fp + "&type=tcp&flow=xtls-rprx-vision"
		if c.Dest != "" {
			base += "&dest=" + url.QueryEscape(c.Dest)
		}
		if c.PublicKey != "" {
			base += "&pbk=" + c.PublicKey
		}
		if c.ShortID != "" {
			base += "&sid=" + c.ShortID
		}
		if c.SpiderX != "" && c.SpiderX != "/" {
			base += "&spx=" + url.QueryEscape(c.SpiderX)
		}
	}
	return base
}

// ToJSON serializes the VLESS configuration to JSON.
func (c *VLESSConfig) ToJSON() ([]byte, error) {
	return json.Marshal(c)
}

// FromJSON deserializes VLESS configuration from JSON.
func (c *VLESSConfig) FromJSON(data []byte) error {
	return json.Unmarshal(data, c)
}

// NodeUUID returns the deterministic VLESS UUID for a node. The UUID is
// derived from the node ID and never changes across restarts. When a
// server secret is configured it keys the derivation (HMAC), so an outsider
// who knows the public namespace and a node's sequential ID cannot compute
// the endpoint UUID offline.
func NodeUUID(nodeID types.NodeID, secret string) string {
	if secret == "" {
		// Fallback used only when no secret is configured (e.g. tests).
		// It is deterministic but publicly enumerable — production always
		// sets a secret via XRayConfig.InitIdentity.
		ns := uuid.FromStringOrNil(vlessUUIDNamespace)
		return uuid.NewV5(ns, fmt.Sprintf("stealthscale:%d", nodeID)).String()
	}
	nsBytes := hmacSum(secret, "uuid-namespace")
	ns := uuid.FromBytesOrNil(nsBytes[:16])
	return uuid.NewV5(ns, fmt.Sprintf("node:%d", nodeID)).String()
}

// NodePort returns the deterministic VLESS listen port for a node, derived
// from a keyed hash of the node ID into the configured range. With a server
// secret this is not enumerable by an outsider.
func NodePort(nodeID types.NodeID, secret string, minPort, maxPort int) int {
	if maxPort <= minPort {
		return minPort
	}

	var sum [32]byte
	if secret == "" {
		sum = sha256.Sum256([]byte(fmt.Sprintf("stealthscale-port:%d", nodeID)))
	} else {
		sum = sha256.Sum256(hmacSum(secret, fmt.Sprintf("node-port:%d", nodeID)))
	}
	hash := binary.BigEndian.Uint64(sum[:8])

	span := uint64(maxPort - minPort + 1)

	return minPort + int(hash%span)
}

// ParseVLESSRequest reads and validates a VLESS request header from the
// connection. It returns the client UUID and a buffered reader that yields
// the remaining stream. The destination carried in the addons (if any) is
// parsed and discarded: the payload is handed to the caller directly.
func ParseVLESSRequest(r io.Reader) (uuidStr string, rest *bufio.Reader, err error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}

	var version [1]byte
	if _, err := io.ReadFull(br, version[:]); err != nil {
		return "", nil, fmt.Errorf("reading VLESS version: %w", err)
	}
	if version[0] != vlessVersion {
		return "", nil, fmt.Errorf("unsupported VLESS version %d", version[0])
	}

	var clientUUID [vlessUUIDLen]byte
	if _, err := io.ReadFull(br, clientUUID[:]); err != nil {
		return "", nil, fmt.Errorf("reading VLESS UUID: %w", err)
	}

	var addonsLen [vlessAddonsLenLen]byte
	if _, err := io.ReadFull(br, addonsLen[:]); err != nil {
		return "", nil, fmt.Errorf("reading VLESS addons length: %w", err)
	}

	if n := int(addonsLen[0]); n > 0 {
		if _, err := io.CopyN(io.Discard, br, int64(n)); err != nil {
			return "", nil, fmt.Errorf("reading VLESS addons: %w", err)
		}
	}

	u, err := uuid.FromBytes(clientUUID[:])
	if err != nil {
		return "", nil, fmt.Errorf("invalid VLESS UUID: %w", err)
	}

	return u.String(), br, nil
}

// ValidateVLESSConfig validates a VLESS configuration.
func ValidateVLESSConfig(config *VLESSConfig) error {
	return config.Validate()
}
