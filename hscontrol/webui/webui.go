// Package webui provides an embedded WebUI similar to headscale-ui,
// built into both server and client (same codebase, no duplication).
// Served at /web and /admin (alias). Uses Go embed + file server.
package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tomiwebpro/stealthscale/hscontrol/derp"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"tailscale.com/tailcfg"
	"tailscale.com/types/views"
)

//go:embed frontend/*
var frontendFS embed.FS

// State is the minimal interface required from hscontrol/state.State.
type State interface {
	ListNodes(nodeIDs ...types.NodeID) views.Slice[types.NodeView]
	ListAllUsers() ([]types.User, error)
	ListPreAuthKeys() ([]types.PreAuthKey, error)
	GetPolicy() (*types.Policy, error)
	DERPMap() tailcfg.DERPMapView
	PingDB(ctx context.Context) error
}

// Handler returns an http.Handler serving the WebUI at /web and /admin.
// It serves embedded static assets and JSON APIs for management.
func Handler(cfg *types.Config, st State) http.Handler {
	mux := http.NewServeMux()

	// Static frontend: serve embedded files at /web/ and /admin/.
	// Use sub FS so http.FileServer sees clean paths.
	sub, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		panic(fmt.Sprintf("webui: sub fs: %v", err))
	}
	fileServer := http.FileServer(http.FS(sub))

	// Serve static assets and SPA fallback.
	serveFrontend := func(w http.ResponseWriter, r *http.Request) {
		// If path is directory or unknown, serve index.html for SPA routing.
		// http.FileServer already does 404 for missing files; we intercept to
		// serve index.html for non-asset paths (optional).
		// For simplicity, if path is "/" or "/index.html" or asset exists, serve.
		// Otherwise fallback to index.html.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			// Let file server handle
			fileServer.ServeHTTP(w, r)
			return
		}
		// Check if file exists
		if _, err := fs.Stat(sub, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for SPA deep links
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	}

	// Frontend at /web/ and /admin/
	mux.HandleFunc("/web/", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/web")
		if r2.URL.Path == "" {
			r2.URL.Path = "/"
		}
		serveFrontend(w, r2)
	})
	mux.HandleFunc("/admin/", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r2.URL.Path, "/admin")
		if r2.URL.Path == "" {
			r2.URL.Path = "/"
		}
		serveFrontend(w, r2)
	})
	// Also serve at exact /web and /admin redirect to slash
	mux.HandleFunc("/web", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/web/", http.StatusFound)
	})
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	// API endpoints - both prefixes
	apiNodes := func(w http.ResponseWriter, r *http.Request) { handleNodes(w, r, st) }
	apiUsers := func(w http.ResponseWriter, r *http.Request) { handleUsers(w, r, st) }
	apiKeys := func(w http.ResponseWriter, r *http.Request) { handlePreAuthKeys(w, r, st) }
	apiPolicy := func(w http.ResponseWriter, r *http.Request) { handlePolicy(w, r, st) }
	apiDERP := func(w http.ResponseWriter, r *http.Request) { handleDERP(w, r, cfg, st) }
	apiVLESS := func(w http.ResponseWriter, r *http.Request) { handleVLESS(w, r, cfg) }
	apiHealth := func(w http.ResponseWriter, r *http.Request) { handleHealth(w, r, cfg, st) }

	for _, prefix := range []string{"/web/api", "/admin/api"} {
		mux.HandleFunc(prefix+"/nodes", apiNodes)
		mux.HandleFunc(prefix+"/users", apiUsers)
		mux.HandleFunc(prefix+"/preauthkeys", apiKeys)
		mux.HandleFunc(prefix+"/policy", apiPolicy)
		mux.HandleFunc(prefix+"/derp", apiDERP)
		mux.HandleFunc(prefix+"/vless/", apiVLESS) // /vless/{id}
		mux.HandleFunc(prefix+"/health", apiHealth)
		// Also support without trailing slash for vless
		mux.HandleFunc(prefix+"/vless", apiVLESS)
	}

	return mux
}

// Register mounts the WebUI handler onto a chi router at /web and /admin.
// Shared by server and client entrypoints — same code path.
func Register(r chi.Router, cfg *types.Config, st State) {
	h := Handler(cfg, st)
	r.Handle("/web", h)
	r.Handle("/web/*", h)
	r.Handle("/admin", h)
	r.Handle("/admin/*", h)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func handleNodes(w http.ResponseWriter, r *http.Request, st State) {
	nodes := st.ListNodes()
	out := make([]map[string]any, 0, nodes.Len())
	for _, n := range nodes.All() {
		ips := []string{}
		if v4 := n.IPv4(); v4.Valid() {
			ips = append(ips, v4.Get().String())
		}
		if v6 := n.IPv6(); v6.Valid() {
			ips = append(ips, v6.Get().String())
		}
		userID := ""
		if uid := n.UserID(); uid.Valid() {
			userID = fmt.Sprintf("%d", uid.Get())
		}
		expiry := ""
		if e := n.Expiry(); e.Valid() {
			expiry = e.Get().Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, map[string]any{
			"id":        n.ID().String(),
			"hostname":  n.Hostname(),
			"givenName": n.GivenName(),
			"userID":    userID,
			"ips":       ips,
			"tags":      n.Tags().AsSlice(),
			"expiry":    expiry,
			"vless": map[string]any{
				"uuid": xray.NodeUUID(n.ID()),
				"port": xray.NodePort(n.ID(), 10001, 10100),
			},
		})
	}
	writeJSON(w, map[string]any{"nodes": out})
}

func handleUsers(w http.ResponseWriter, r *http.Request, st State) {
	users, err := st.ListAllUsers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id":          u.ID,
			"name":        u.Name,
			"displayName": u.DisplayName,
			"email":       u.Email,
			"provider":    u.Provider,
		})
	}
	writeJSON(w, map[string]any{"users": out})
}

func handlePreAuthKeys(w http.ResponseWriter, r *http.Request, st State) {
	keys, err := st.ListPreAuthKeys()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		userID := ""
		if k.UserID != nil {
			userID = fmt.Sprintf("%d", *k.UserID)
		}
		expiry := ""
		if k.Expiration != nil {
			expiry = k.Expiration.Format("2006-01-02T15:04:05Z07:00")
		}
		created := ""
		if k.CreatedAt != nil {
			created = k.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, map[string]any{
			"id":        k.ID,
			"key":       k.Key,
			"prefix":    k.Prefix,
			"userID":    userID,
			"reusable":  k.Reusable,
			"ephemeral": k.Ephemeral,
			"used":      k.Used,
			"expiry":    expiry,
			"createdAt": created,
		})
	}
	writeJSON(w, map[string]any{"preAuthKeys": out})
}

func handlePolicy(w http.ResponseWriter, r *http.Request, st State) {
	pol, err := st.GetPolicy()
	if err != nil {
		// If no policy, return empty
		writeJSON(w, map[string]any{"policy": "", "error": err.Error()})
		return
	}
	if pol == nil {
		writeJSON(w, map[string]any{"policy": ""})
		return
	}
	writeJSON(w, map[string]any{"policy": pol.Data})
}

func handleDERP(w http.ResponseWriter, r *http.Request, cfg *types.Config, st State) {
	dm := st.DERPMap()
	// Convert DERPMapView to serializable form via tailcfg.DERPMap
	var dmVal *tailcfg.DERPMap
	if dm.Valid() {
		m := dm.AsStruct()
		dmVal = m
	}
	stealthSatisfied := derp.IsStealthSatisfied(&cfg.XRay)
	shouldInclude := derp.ShouldIncludeDERP(cfg)
	writeJSON(w, map[string]any{
		"derpMap":           dmVal,
		"stealth_satisfied": stealthSatisfied,
		"shouldIncludeDERP": shouldInclude,
		"xray": map[string]any{
			"enabled":  cfg.XRay.Enabled,
			"security": cfg.XRay.Security,
			"dest":     cfg.XRay.Reality.Dest,
		},
	})
}

func handleVLESS(w http.ResponseWriter, r *http.Request, cfg *types.Config) {
	// Expect /web/api/vless/{id} or /admin/api/vless/{id}
	// Extract id from path suffix
	path := r.URL.Path
	// Find last segment after /vless/
	var idStr string
	if idx := strings.Index(path, "/vless/"); idx != -1 {
		idStr = strings.TrimPrefix(path[idx+len("/vless/"):], "/")
		// Also handle query param fallback: strip any further slash
		if slash := strings.Index(idStr, "/"); slash != -1 {
			idStr = idStr[:slash]
		}
	} else {
		// Try query param ?id=
		idStr = r.URL.Query().Get("id")
	}
	if idStr == "" {
		http.Error(w, "missing node id", http.StatusBadRequest)
		return
	}
	nodeIDInt, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid node id", http.StatusBadRequest)
		return
	}
	nodeID := types.NodeID(nodeIDInt)
	sec := cfg.XRay.Security
	if sec == "" {
		sec = "reality_xtls"
	}
	if sec == "reality" {
		sec = "reality_xtls"
	}
	vlessCfg := &xray.VLESSConfig{
		ID:       xray.NodeUUID(nodeID),
		Network:  "tcp",
		Address:  cfg.XRay.ListenAddr,
		Port:     xray.NodePort(nodeID, cfg.XRay.BaseListenPort, cfg.XRay.MaxListenPort),
		Security: sec,
		Timeout:  cfg.XRay.Timeout,
	}
	writeJSON(w, map[string]any{
		"nodeID":  nodeID.String(),
		"uuid":    vlessCfg.ID,
		"port":    vlessCfg.Port,
		"uri":     vlessCfg.URI(),
		"config":  vlessCfg,
		"address": vlessCfg.Address,
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request, cfg *types.Config, st State) {
	w.Header().Set("Content-Type", "application/json")
	dbErr := st.PingDB(r.Context())
	status := "pass"
	code := http.StatusOK
	if dbErr != nil {
		status = "fail"
		code = http.StatusInternalServerError
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":            status,
		"derp":                st.DERPMap().AsStruct(),
		"stealth_satisfied": derp.IsStealthSatisfied(&cfg.XRay),
		"xray": map[string]any{
			"enabled":  cfg.XRay.Enabled,
			"security": cfg.XRay.Security,
		},
	})
}
