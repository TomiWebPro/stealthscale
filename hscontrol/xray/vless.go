// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package xray

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
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

	// vlessUUIDNamespace is the namespace used to derive a stable,
	// deterministic UUID per node. Deriving (rather than storing) the
	// UUID keeps it stable across server restarts without a database
	// migration: the operator can hand the UUID to a client and it stays
	// valid for the lifetime of the node.
	vlessUUIDNamespace = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
)

// VLESSConfig represents the configuration for a VLESS endpoint.
type VLESSConfig struct {
	ID       string        `json:"id"`                // UUID for authentication
	Network  string        `json:"network"`           // Network type (tcp, ws, etc.)
	Address  string        `json:"address"`           // Listen address
	Port     int           `json:"port"`              // Listen port
	Security string        `json:"security"`          // Security setting (none, tls, xtls)
	Alpn     string        `json:"alpn,omitempty"`    // ALPN for TLS
	Timeout  time.Duration `json:"timeout,omitempty"` // Connection timeout
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
	switch c.Security {
	case "none", "tls", "xtls":
		// Valid
	default:
		return fmt.Errorf("invalid security mode: %s", c.Security)
	}
	return nil
}

// URI returns the URI form of the VLESS endpoint, e.g.
// vless://<uuid>@<address>:<port>?security=<security>, suitable for
// distribution to clients.
func (c *VLESSConfig) URI() string {
	return fmt.Sprintf("vless://%s@%s:%d?security=%s", c.ID, c.Address, c.Port, c.Security)
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
// derived from the node ID and never changes across restarts.
func NodeUUID(nodeID types.NodeID) string {
	ns := uuid.FromStringOrNil(vlessUUIDNamespace)
	return uuid.NewV5(ns, fmt.Sprintf("stealthscale:%d", nodeID)).String()
}

// NodePort returns the deterministic VLESS listen port for a node, derived
// from a hash of the node ID into the configured range.
func NodePort(nodeID types.NodeID, minPort, maxPort int) int {
	if maxPort <= minPort {
		return minPort
	}

	sum := sha256.Sum256([]byte(fmt.Sprintf("stealthscale-port:%d", nodeID)))
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
