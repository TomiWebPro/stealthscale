// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package hscontrol

import (
	"context"
	"fmt"
	"net"

	"github.com/rs/zerolog/log"
	"tailscale.com/control/controlbase"
	"tailscale.com/types/key"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
)

// StartXRayServer boots the VLESS transport listeners. When enabled, every
// registered node gets a dedicated listener on a deterministic port,
// authenticated by a deterministic UUID derived from the node ID. Clients
// dial the VLESS endpoint and speak the Tailscale noise protocol over the
// authenticated stream.
//
// [StealthScale.Serve] calls this internally; it is exported so tests can start
// the transport without going through the full server startup.
func (h *StealthScale) StartXRayServer(ctx context.Context) error {
	if !h.cfg.XRay.Enabled {
		return nil
	}

	s, err := xray.NewServer(&h.cfg.XRay, h.serveVLESSConn)
	if err != nil {
		return fmt.Errorf("creating xray server: %w", err)
	}
	h.xrayServer = s

	log.Info().
		Str("listen_addr", h.cfg.XRay.ListenAddr).
		Int("listen_port", h.cfg.XRay.BaseListenPort).
		Int("max_listen_port", h.cfg.XRay.MaxListenPort).
		Str("security", h.cfg.XRay.Security).
		Msg("xray VLESS transport enabled")

	nodes := h.state.ListNodes()
	for _, node := range nodes.All() {
		h.ensureXRayListenerForNode(node.ID())
	}

	return nil
}

// serveVLESSConn runs the Tailscale noise handshake over an authenticated
// VLESS stream and serves the machine API over it.
func (h *StealthScale) serveVLESSConn(conn net.Conn) {
	noiseConn, err := controlbase.Server(context.Background(), conn, *h.noisePrivateKey, nil)
	if err != nil {
		log.Error().
			Str("remote_addr", conn.RemoteAddr().String()).
			Err(err).
			Msg("noise handshake over VLESS failed")
		return
	}

	ns := noiseServer{
		stealthscale: h,
		challenge: key.NewChallenge(),
	}

	h.serveNoise(ns, noiseConn, conn.RemoteAddr().String())
}

// ensureXRayListenerForNode starts the VLESS listener for a node if the
// xray transport is enabled and no listener is running yet.
func (h *StealthScale) ensureXRayListenerForNode(nodeID types.NodeID) {
	if h.xrayServer == nil {
		return
	}

	if _, err := h.xrayServer.EnsureNodeListener(context.Background(), nodeID); err != nil {
		log.Error().
			Uint64("node_id", uint64(nodeID)).
			Err(err).
			Msg("starting xray listener for node")
	}
}
