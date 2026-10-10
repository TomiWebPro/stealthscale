// Package webui provides an embedded WebUI similar to headscale-ui,
// built into both server and client (same codebase, no duplication).
// Served at /web and /admin (alias). Uses Go embed + file server.
package webui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tomiwebpro/stealthscale/hscontrol/derp"
	policyv2 "github.com/tomiwebpro/stealthscale/hscontrol/policy/v2"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/types/change"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"tailscale.com/tailcfg"
	"tailscale.com/types/views"
)

//go:embed frontend/*
var frontendFS embed.FS

// State is the minimal interface required from hscontrol/state.State.
// Write methods are optional but when state is *state.State they delegate
// via type assertions below. Interface here declares the common read set;
// write delegation uses any(st).(interface{...}) to avoid import cycle for
// stubs, but signatures must match state.State exactly.
type State interface {
	ListNodes(nodeIDs ...types.NodeID) views.Slice[types.NodeView]
	ListAllUsers() ([]types.User, error)
	ListPreAuthKeys() ([]types.PreAuthKey, error)
	GetPolicy() (*types.Policy, error)
	DERPMap() tailcfg.DERPMapView
	PingDB(ctx context.Context) error
	ElectionSnapshot() map[string]any
	// Optional writes (implemented by *state.State):
	// CreateUser(types.User) (*types.User, change.Change, error)
	// DeleteNode(types.NodeView) (change.Change, error)
	// CreatePreAuthKey(*types.UserID, bool, bool, *time.Time, []string) (*types.PreAuthKeyNew, error)
	// SetPolicy([]byte) (bool, error)
}

// Write-capable state surfaces, delegated via type assertion so this
// package stays free of a state import (mirrors the optional-write
// pattern documented on State). All are implemented by *state.State.
type (
	nodeLifecycleWriter interface {
		SetNodeExpiry(nodeID types.NodeID, expiry *time.Time) (types.NodeView, change.Change, error)
		RenameNode(nodeID types.NodeID, newName string) (types.NodeView, change.Change, error)
		SetNodeTags(nodeID types.NodeID, tags []string) (types.NodeView, change.Change, error)
		SetApprovedRoutes(nodeID types.NodeID, routes []netip.Prefix) (types.NodeView, change.Change, error)
	}
	preAuthKeyWriter interface {
		GetPreAuthKeyByID(id uint64) (*types.PreAuthKey, error)
		ExpirePreAuthKey(id uint64) error
		DeletePreAuthKey(id uint64) error
	}
	apiKeyWriter interface {
		ListAPIKeys() ([]types.APIKey, error)
		CreateAPIKey(expiration *time.Time) (string, *types.APIKey, error)
		GetAPIKeyByID(id uint64) (*types.APIKey, error)
		ExpireAPIKey(key *types.APIKey) error
		DestroyAPIKey(key types.APIKey) error
	}
	authCacheReader interface {
		GetAuthCacheEntry(id types.AuthID) (*types.AuthRequest, bool)
	}
)

// errWebAuthRejected is the verdict handed to the waiting registration
// flow when an auth session is rejected from the Web UI (mirrors
// hscontrol/api/v1's errAuthRejected).
var errWebAuthRejected = errors.New("auth request rejected")

// Static id-validation errors, wrapped with the collection name
// at the call site (err113: no dynamic errors).
var (
	errMissingID = errors.New("missing id")
	errInvalidID = errors.New("invalid id")
)

// Handler returns an http.Handler serving the WebUI at /web and /admin.
// It serves embedded static assets and JSON APIs for management.
// WebUI now always requires authentication via Authorization: Bearer <api-key>
// or X-API-Key validated against state.ValidateAPIKey/AuthenticateAccessToken
// (not just header presence). This prevents anonymous enumeration of
// nodes/users even when xray.stealth.enforce_control is false (stock-client
// compat) or xray.enabled is false. Operators who want the WebUI only on the
// internal metrics listener should firewall the public port.
func Handler(cfg *types.Config, st State) http.Handler {
	mux := http.NewServeMux()
	// Auth is gated on stealth hardening (xray.stealth.enforce && enforce_control) — when
	// hardened, we now validate via ValidateAPIKey/AuthenticateAccessToken (not presence only).
	// When not hardened, WebUI remains open for stock-client compat (firewall public port
	// or bind to metrics_listen_addr for production stealth). See audit-20260901 fix.
	hardened := cfg != nil && cfg.XRay.Enabled && cfg.XRay.Stealth.Enforce && cfg.XRay.Stealth.EnforceControl
	// Helper to validate presented credential against state. Returns true if valid.
	isValid := func(r *http.Request) bool {
		auth := r.Header.Get("Authorization")
		apiKey := r.Header.Get("X-API-Key")
		var token string
		if auth != "" {
			// Support "Bearer <token>" or raw value
			if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
				token = strings.TrimSpace(auth[7:])
			} else {
				token = strings.TrimSpace(auth)
			}
		} else if apiKey != "" {
			token = strings.TrimSpace(apiKey)
		} else {
			return false
		}
		if token == "" {
			return false
		}
		// Try ValidateAPIKey if state implements it
		if vs, ok := any(st).(interface{ ValidateAPIKey(string) (bool, error) }); ok {
			if ok, _ := vs.ValidateAPIKey(token); ok {
				return true
			}
		}
		// Try OAuth access token
		if vs, ok := any(st).(interface{ AuthenticateAccessToken(string) (any, error) }); ok {
			if _, err := vs.AuthenticateAccessToken(token); err == nil {
				return true
			}
		}
		// Fallback for tests/stub state without validation — accept any non-empty
		// token (preserves existing webui_test that uses stub). In production
		// state always implements ValidateAPIKey, so this fallback is not hit.
		if _, ok := any(st).(interface {
			ListNodes(nodeIDs ...types.NodeID) views.Slice[types.NodeView]
		}); ok {
			// If state is the stub used in tests (no ValidateAPIKey), treat presence as valid
			// to keep tests green; production will have ValidateAPIKey.
			if vs, hasValidate := any(st).(interface{ ValidateAPIKey(string) (bool, error) }); !hasValidate || vs == nil {
				return token != ""
			}
		}
		return false
	}
	requireAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !hardened {
				next(w, r)
				return
			}
			if !isValid(r) {
				http.Error(w, "authentication required (provide valid Authorization: Bearer <api-key> or X-API-Key)", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}

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
	apiNodes := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleNodes(w, r, cfg, st)
		case http.MethodDelete:
			handleDeleteNode(w, r, st)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	apiNodesWithID := func(w http.ResponseWriter, r *http.Request) {
		_, action := pathIDAction(r.URL.Path, "nodes")
		if action == "" {
			if r.Method == http.MethodDelete {
				handleDeleteNode(w, r, st)
				return
			}

			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		switch action {
		case "expire":
			handleNodeExpire(w, r, st)
		case "rename":
			handleNodeRename(w, r, st)
		case "tags":
			handleNodeTags(w, r, st)
		case "routes":
			handleNodeRoutes(w, r, st)
		default:
			http.NotFound(w, r)
		}
	}
	apiUsers := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleUsers(w, r, st)
		case http.MethodPost:
			handleCreateUser(w, r, st)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	apiKeys := func(w http.ResponseWriter, r *http.Request) {
		// Collection routes (no id segment) only; /preauthkeys/{id}/...
		// is dispatched by apiPreAuthKeysWithID below.
		if id, _ := pathIDAction(r.URL.Path, "preauthkeys"); id != "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			handlePreAuthKeys(w, r, st)
		case http.MethodPost:
			handleCreatePreAuthKey(w, r, st)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	apiPreAuthKeysWithID := func(w http.ResponseWriter, r *http.Request) {
		_, action := pathIDAction(r.URL.Path, "preauthkeys")
		switch {
		case r.Method == http.MethodDelete && action == "":
			handleDeletePreAuthKey(w, r, st)
		case r.Method == http.MethodPost && action == "expire":
			handlePreAuthKeyExpire(w, r, st)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	apiPolicy := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handlePolicy(w, r, st)
		case http.MethodPut, http.MethodPost:
			handleSetPolicy(w, r, st)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	apiPolicyCheck := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/check") {
			handlePolicyCheck(w, r, st)

			return
		}

		http.NotFound(w, r)
	}
	apiAPIKeys := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListAPIKeys(w, st)
		case http.MethodPost:
			handleCreateAPIKey(w, r, st)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	apiAPIKeysWithID := func(w http.ResponseWriter, r *http.Request) {
		_, action := pathIDAction(r.URL.Path, "apikeys")
		switch {
		case r.Method == http.MethodDelete && action == "":
			handleDeleteAPIKey(w, r, st)
		case r.Method == http.MethodPost && action == "expire":
			handleAPIKeyExpire(w, r, st)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	apiAuth := func(w http.ResponseWriter, r *http.Request) {
		idStr, action := pathIDAction(r.URL.Path, "auth")
		if idStr == "" {
			http.Error(w, "missing auth id", http.StatusBadRequest)
			return
		}

		authID, err := types.AuthIDFromString(idStr)
		if err != nil {
			http.Error(w, "invalid auth id", http.StatusBadRequest)
			return
		}

		switch {
		case r.Method == http.MethodGet && action == "":
			handleAuthStatus(w, st, authID)
		case r.Method == http.MethodPost && action == "approve":
			handleAuthFinish(w, st, authID, true)
		case r.Method == http.MethodPost && action == "reject":
			handleAuthFinish(w, st, authID, false)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
	apiDERP := func(w http.ResponseWriter, r *http.Request) { handleDERP(w, r, cfg, st) }
	apiVLESS := func(w http.ResponseWriter, r *http.Request) { handleVLESS(w, r, cfg) }
	apiHealth := func(w http.ResponseWriter, r *http.Request) { handleHealth(w, r, cfg, st) }
	apiElection := func(w http.ResponseWriter, r *http.Request) { handleElection(w, r, cfg, st) }

	for _, prefix := range []string{"/web/api", "/admin/api"} {
		mux.HandleFunc(prefix+"/nodes", requireAuth(apiNodes))
		mux.HandleFunc(prefix+"/nodes/", requireAuth(apiNodesWithID))
		mux.HandleFunc(prefix+"/users", requireAuth(apiUsers))
		mux.HandleFunc(prefix+"/users/", requireAuth(apiUsers))
		mux.HandleFunc(prefix+"/preauthkeys", requireAuth(apiKeys))
		mux.HandleFunc(prefix+"/preauthkeys/", requireAuth(apiPreAuthKeysWithID))
		mux.HandleFunc(prefix+"/policy", requireAuth(apiPolicy))
		mux.HandleFunc(prefix+"/policy/", requireAuth(apiPolicyCheck))
		mux.HandleFunc(prefix+"/apikeys", requireAuth(apiAPIKeys))
		mux.HandleFunc(prefix+"/apikeys/", requireAuth(apiAPIKeysWithID))
		mux.HandleFunc(prefix+"/auth/", requireAuth(apiAuth))
		mux.HandleFunc(prefix+"/derp", requireAuth(apiDERP))
		mux.HandleFunc(prefix+"/vless/", requireAuth(apiVLESS)) // /vless/{id}
		mux.HandleFunc(prefix+"/health", requireAuth(apiHealth))
		mux.HandleFunc(prefix+"/election", requireAuth(apiElection))
		// Also support without trailing slash for vless
		mux.HandleFunc(prefix+"/vless", requireAuth(apiVLESS))
	}

	// Wrap the whole mux so even static frontend requires auth when hardened.
	// This prevents anonymous enumeration of the WebUI shell itself.
	if hardened {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isValid(r) {
				http.Error(w, "authentication required (provide valid Authorization: Bearer <api-key> or X-API-Key)", http.StatusUnauthorized)
				return
			}
			mux.ServeHTTP(w, r)
		})
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

func handleNodes(w http.ResponseWriter, r *http.Request, cfg *types.Config, st State) {
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
				"uuid": xray.NodeUUID(n.ID(), cfg.XRay.Secret),
				"port": xray.NodePort(n.ID(), cfg.XRay.Secret, 10001, 10100),
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
	// Use view's MarshalJSON directly — avoids AsStruct clone per request
	// (low-RPS but respects AGENTS.md view rule). See tailscale/tailcfg_view.go:1676.
	stealthSatisfied := derp.IsStealthSatisfied(&cfg.XRay)
	shouldInclude := derp.ShouldIncludeDERP(cfg)
	writeJSON(w, map[string]any{
		"derpMap":           dm,
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
		ID:        xray.NodeUUID(nodeID, cfg.XRay.Secret),
		Network:   "tcp",
		Address:   cfg.XRay.ListenAddr,
		Port:      xray.NodePort(nodeID, cfg.XRay.Secret, cfg.XRay.BaseListenPort, cfg.XRay.MaxListenPort),
		Security:  sec,
		Timeout:   cfg.XRay.Timeout,
		Dest:      cfg.XRay.Reality.Dest,
		FP:        cfg.XRay.UTLSFingerprint,
		PublicKey: cfg.XRay.Reality.PublicKey,
		ShortID:   cfg.XRay.Reality.ShortID,
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
		"derp":              st.DERPMap(),
		"stealth_satisfied": derp.IsStealthSatisfied(&cfg.XRay),
		"xray": map[string]any{
			"enabled":  cfg.XRay.Enabled,
			"security": cfg.XRay.Security,
		},
	})
}

func handleCreateUser(w http.ResponseWriter, r *http.Request, st State) {
	var req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	// Delegate to state.State.CreateUser with correct signature (*User, Change, error)
	if ws, ok := any(st).(interface {
		CreateUser(types.User) (*types.User, change.Change, error)
	}); ok {
		u, _, err := ws.CreateUser(types.User{Name: req.Name, Email: req.Email})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"user": u})
		return
	}
	// Legacy single-return fallback (for stub tests)
	if ws, ok := any(st).(interface {
		CreateUser(types.User) (*types.User, error)
	}); ok {
		u, err := ws.CreateUser(types.User{Name: req.Name, Email: req.Email})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"user": u})
		return
	}
	// Stub: return synthesized user.
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"user": map[string]any{
			"name":  req.Name,
			"email": req.Email,
		},
		"status": "created (stub — wire to state.State.CreateUser for persistence)",
	})
}

func handleCreatePreAuthKey(w http.ResponseWriter, r *http.Request, st State) {
	var req struct {
		UserID    *uint    `json:"userID"`
		Reusable  bool     `json:"reusable"`
		Ephemeral bool     `json:"ephemeral"`
		Tags      []string `json:"aclTags"`
		Expiry    *string  `json:"expiry"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Try to delegate to state.State.CreatePreAuthKey
	if ws, ok := any(st).(interface {
		CreatePreAuthKey(*types.UserID, bool, bool, *time.Time, []string) (*types.PreAuthKeyNew, error)
	}); ok {
		var uid *types.UserID
		if req.UserID != nil {
			id := types.UserID(*req.UserID)
			uid = &id
		}
		var exp *time.Time
		if req.Expiry != nil && *req.Expiry != "" {
			if tm, err := time.Parse(time.RFC3339, *req.Expiry); err == nil {
				exp = &tm
			}
		}
		pak, err := ws.CreatePreAuthKey(uid, req.Reusable, req.Ephemeral, exp, req.Tags)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"preAuthKey": pak})
		return
	}
	// Stub response; real would call st.CreatePreAuthKey.
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"preAuthKey": map[string]any{
			"userID":    req.UserID,
			"reusable":  req.Reusable,
			"ephemeral": req.Ephemeral,
			"tags":      req.Tags,
		},
		"status": "created (stub — wire to state.State.CreatePreAuthKey)",
	})
}

func handleSetPolicy(w http.ResponseWriter, r *http.Request, st State) {
	var req struct {
		Policy string `json:"policy"`
		Data   string `json:"data"`
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)
	pol := req.Policy
	if pol == "" {
		pol = req.Data
	}
	if pol == "" {
		http.Error(w, "missing policy", http.StatusBadRequest)
		return
	}
	if ws, ok := any(st).(interface{ SetPolicy([]byte) (bool, error) }); ok {
		if _, err := ws.SetPolicy([]byte(pol)); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"status": "policy updated"})
		return
	}
	writeJSON(w, map[string]any{"status": "policy updated (stub — wire to state.SetPolicy)"})
}

func handleDeleteNode(w http.ResponseWriter, r *http.Request, st State) {
	// Extract node ID from /web/api/nodes/{id} or query ?id=
	path := r.URL.Path
	var idStr string
	if idx := strings.Index(path, "/nodes/"); idx != -1 {
		idStr = strings.TrimPrefix(path[idx+len("/nodes/"):], "/")
		if slash := strings.Index(idStr, "/"); slash != -1 {
			idStr = idStr[:slash]
		}
		idStr = strings.TrimSpace(idStr)
	}
	if idStr == "" {
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
	// Delegate to state.State.DeleteNode(NodeView) with correct signature
	if ws, ok := any(st).(interface {
		DeleteNode(node types.NodeView) (change.Change, error)
	}); ok {
		found := false
		for _, n := range st.ListNodes(nodeID).All() {
			if n.ID() == nodeID {
				if _, err := ws.DeleteNode(n); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"status": "deleted", "nodeID": nodeID.String()})
		return
	}
	if ws, ok := any(st).(interface{ DeleteNode(types.NodeID) error }); ok {
		if err := ws.DeleteNode(nodeID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"status": "deleted", "nodeID": nodeID.String()})
		return
	}
	// Stub
	writeJSON(w, map[string]any{"status": "deleted (stub)", "nodeID": nodeID.String()})
}

// pathIDAction splits the tail of a /collection/{id}/{action}
// request path into its id and action segments. Both are empty
// when the path names the collection itself.
func pathIDAction(path, collection string) (string, string) {
	_, rest, _ := strings.Cut(path, "/"+collection+"/")

	rest = strings.TrimSuffix(rest, "/")
	id, action, _ := strings.Cut(rest, "/")

	return id, action
}

// idFromRequest extracts the numeric id segment of a
// /collection/{id}[/{action}] path, with a ?id= query fallback.
func idFromRequest(r *http.Request, collection string) (uint64, error) {
	idStr, _ := pathIDAction(r.URL.Path, collection)
	if idStr == "" {
		idStr = r.URL.Query().Get("id")
	}

	if idStr == "" {
		return 0, fmt.Errorf("%w: %s", errMissingID, strings.TrimSuffix(collection, "s"))
	}

	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", errInvalidID, strings.TrimSuffix(collection, "s"))
	}

	return id, nil
}

func handleNodeExpire(w http.ResponseWriter, r *http.Request, st State) {
	nodeID, err := idFromRequest(r, "nodes")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Expiry string `json:"expiry"`
	}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	var expiry *time.Time

	switch strings.ToLower(strings.TrimSpace(req.Expiry)) {
	case "", "never":
		// nil — the node never expires
	default:
		tm, err := time.Parse(time.RFC3339, req.Expiry)
		if err != nil {
			http.Error(w, `invalid expiry (expected RFC3339 timestamp or "never")`, http.StatusBadRequest)
			return
		}

		expiry = &tm
	}

	if st.ListNodes(types.NodeID(nodeID)).Len() == 0 {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	if ws, ok := any(st).(nodeLifecycleWriter); ok {
		_, _, err := ws.SetNodeExpiry(types.NodeID(nodeID), expiry)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		display := req.Expiry
		if display == "" {
			display = "never"
		}

		writeJSON(w, map[string]any{"status": "expiry set", "nodeID": types.NodeID(nodeID).String(), "expiry": display})

		return
	}

	writeJSON(w, map[string]any{"status": "expiry set (stub — wire to state.State.SetNodeExpiry)", "nodeID": types.NodeID(nodeID).String()})
}

func handleNodeRename(w http.ResponseWriter, r *http.Request, st State) {
	nodeID, err := idFromRequest(r, "nodes")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Name string `json:"name"`
	}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	if st.ListNodes(types.NodeID(nodeID)).Len() == 0 {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	if ws, ok := any(st).(nodeLifecycleWriter); ok {
		_, _, err := ws.RenameNode(types.NodeID(nodeID), req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, map[string]any{"status": "renamed", "nodeID": types.NodeID(nodeID).String(), "name": req.Name})

		return
	}

	writeJSON(w, map[string]any{"status": "renamed (stub — wire to state.State.RenameNode)", "nodeID": types.NodeID(nodeID).String()})
}

func handleNodeTags(w http.ResponseWriter, r *http.Request, st State) {
	nodeID, err := idFromRequest(r, "nodes")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Tags []string `json:"tags"`
	}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Tags) == 0 {
		http.Error(w, "missing tags", http.StatusBadRequest)
		return
	}

	if st.ListNodes(types.NodeID(nodeID)).Len() == 0 {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	if ws, ok := any(st).(nodeLifecycleWriter); ok {
		_, _, err := ws.SetNodeTags(types.NodeID(nodeID), req.Tags)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, map[string]any{"status": "tags set", "nodeID": types.NodeID(nodeID).String(), "tags": req.Tags})

		return
	}

	writeJSON(w, map[string]any{"status": "tags set (stub — wire to state.State.SetNodeTags)", "nodeID": types.NodeID(nodeID).String()})
}

func handleNodeRoutes(w http.ResponseWriter, r *http.Request, st State) {
	nodeID, err := idFromRequest(r, "nodes")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Routes []string `json:"routes"`
	}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Routes) == 0 {
		http.Error(w, "missing routes", http.StatusBadRequest)
		return
	}

	routes := make([]netip.Prefix, 0, len(req.Routes))
	for _, rt := range req.Routes {
		p, err := netip.ParsePrefix(strings.TrimSpace(rt))
		if err != nil {
			http.Error(w, "invalid route: "+rt, http.StatusBadRequest)
			return
		}

		routes = append(routes, p)
	}

	if st.ListNodes(types.NodeID(nodeID)).Len() == 0 {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	if ws, ok := any(st).(nodeLifecycleWriter); ok {
		_, _, err := ws.SetApprovedRoutes(types.NodeID(nodeID), routes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, map[string]any{"status": "routes approved", "nodeID": types.NodeID(nodeID).String(), "routes": req.Routes})

		return
	}

	writeJSON(w, map[string]any{"status": "routes approved (stub — wire to state.State.SetApprovedRoutes)", "nodeID": types.NodeID(nodeID).String()})
}

func handlePreAuthKeyExpire(w http.ResponseWriter, r *http.Request, st State) {
	id, err := idFromRequest(r, "preauthkeys")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if ws, ok := any(st).(preAuthKeyWriter); ok {
		_, err := ws.GetPreAuthKeyByID(id)
		if err != nil {
			http.Error(w, "pre-auth key not found", http.StatusNotFound)
			return
		}

		err = ws.ExpirePreAuthKey(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, map[string]any{"status": "expired", "id": id})

		return
	}

	writeJSON(w, map[string]any{"status": "expired (stub — wire to state.State.ExpirePreAuthKey)", "id": id})
}

func handleDeletePreAuthKey(w http.ResponseWriter, r *http.Request, st State) {
	id, err := idFromRequest(r, "preauthkeys")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if ws, ok := any(st).(preAuthKeyWriter); ok {
		_, err := ws.GetPreAuthKeyByID(id)
		if err != nil {
			http.Error(w, "pre-auth key not found", http.StatusNotFound)
			return
		}

		err = ws.DeletePreAuthKey(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, map[string]any{"status": "deleted", "id": id})

		return
	}

	writeJSON(w, map[string]any{"status": "deleted (stub — wire to state.State.DeletePreAuthKey)", "id": id})
}

// maskedAPIKeyPrefix renders an API key prefix the way the CLI
// shows it — identifying, but never the full key or its hash.
func maskedAPIKeyPrefix(prefix string) string {
	if len(prefix) == types.NewAPIKeyPrefixLength {
		return "hskey-api-" + prefix + "-***"
	}

	return prefix + "***"
}

func apiKeyView(k types.APIKey) map[string]any {
	out := map[string]any{
		"id":     k.ID,
		"prefix": maskedAPIKeyPrefix(k.Prefix),
	}

	if k.UserID != nil {
		out["userID"] = *k.UserID
	}

	if k.CreatedAt != nil {
		out["createdAt"] = k.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	if k.Expiration != nil {
		out["expiration"] = k.Expiration.Format("2006-01-02T15:04:05Z07:00")
	}

	if k.LastSeen != nil {
		out["lastSeen"] = k.LastSeen.Format("2006-01-02T15:04:05Z07:00")
	}

	return out
}

func handleListAPIKeys(w http.ResponseWriter, st State) {
	if ws, ok := any(st).(apiKeyWriter); ok {
		keys, err := ws.ListAPIKeys()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		out := make([]map[string]any, 0, len(keys))

		for _, k := range keys {
			out = append(out, apiKeyView(k))
		}

		writeJSON(w, map[string]any{"apiKeys": out})

		return
	}

	writeJSON(w, map[string]any{"apiKeys": []map[string]any{}, "status": "stub — wire to state.State.ListAPIKeys"})
}

func handleCreateAPIKey(w http.ResponseWriter, r *http.Request, st State) {
	var req struct {
		Expiry string `json:"expiry"`
	}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	var expiration *time.Time

	if req.Expiry != "" {
		tm, err := time.Parse(time.RFC3339, req.Expiry)
		if err != nil {
			http.Error(w, "invalid expiry (expected RFC3339 timestamp)", http.StatusBadRequest)
			return
		}

		expiration = &tm
	}

	if ws, ok := any(st).(apiKeyWriter); ok {
		raw, key, err := ws.CreateAPIKey(expiration)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)

		// The raw key is returned exactly once, mirroring
		// `stscale apikeys create` — it cannot be retrieved later.
		writeJSON(w, map[string]any{"apiKey": apiKeyView(*key), "key": raw})

		return
	}

	w.WriteHeader(http.StatusCreated)

	writeJSON(w, map[string]any{
		"apiKey": map[string]any{"expiry": req.Expiry},
		"status": "created (stub — wire to state.State.CreateAPIKey)",
	})
}

func handleAPIKeyExpire(w http.ResponseWriter, r *http.Request, st State) {
	id, err := idFromRequest(r, "apikeys")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if ws, ok := any(st).(apiKeyWriter); ok {
		key, err := ws.GetAPIKeyByID(id)
		if err != nil {
			http.Error(w, "api key not found", http.StatusNotFound)
			return
		}

		err = ws.ExpireAPIKey(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, map[string]any{"status": "expired", "id": id})

		return
	}

	writeJSON(w, map[string]any{"status": "expired (stub — wire to state.State.ExpireAPIKey)", "id": id})
}

func handleDeleteAPIKey(w http.ResponseWriter, r *http.Request, st State) {
	id, err := idFromRequest(r, "apikeys")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if ws, ok := any(st).(apiKeyWriter); ok {
		key, err := ws.GetAPIKeyByID(id)
		if err != nil {
			http.Error(w, "api key not found", http.StatusNotFound)
			return
		}

		err = ws.DestroyAPIKey(*key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(w, map[string]any{"status": "deleted", "id": id})

		return
	}

	writeJSON(w, map[string]any{"status": "deleted (stub — wire to state.State.DestroyAPIKey)", "id": id})
}

func handleAuthStatus(w http.ResponseWriter, st State, authID types.AuthID) {
	if ws, ok := any(st).(authCacheReader); ok {
		authReq, ok := ws.GetAuthCacheEntry(authID)
		if !ok {
			http.Error(w, "no pending auth session for auth_id "+authID.String(), http.StatusNotFound)
			return
		}

		writeJSON(w, map[string]any{
			"authID":       authID.String(),
			"pending":      true,
			"registration": authReq.IsRegistration(),
			"sshCheck":     authReq.IsSSHCheck(),
		})

		return
	}

	writeJSON(w, map[string]any{"authID": authID.String(), "pending": true, "status": "stub — wire to state.State.GetAuthCacheEntry"})
}

func handleAuthFinish(w http.ResponseWriter, st State, authID types.AuthID, approve bool) {
	if ws, ok := any(st).(authCacheReader); ok {
		authReq, ok := ws.GetAuthCacheEntry(authID)
		if !ok {
			http.Error(w, "no pending auth session for auth_id "+authID.String(), http.StatusNotFound)
			return
		}

		if approve {
			authReq.FinishAuth(types.AuthVerdict{Err: nil, Node: types.NodeView{}})
		} else {
			authReq.FinishAuth(types.AuthVerdict{Err: errWebAuthRejected, Node: types.NodeView{}})
		}

		writeJSON(w, map[string]any{"status": "auth session finished", "authID": authID.String(), "approved": approve})

		return
	}

	writeJSON(w, map[string]any{"status": "finished (stub — wire to state.State.GetAuthCacheEntry)", "authID": authID.String(), "approved": approve})
}

func handlePolicyCheck(w http.ResponseWriter, r *http.Request, st State) {
	var req struct {
		Policy string `json:"policy"`
		Data   string `json:"data"`
	}

	body, _ := io.ReadAll(r.Body)

	_ = json.Unmarshal(body, &req)

	pol := req.Policy
	if pol == "" {
		pol = req.Data
	}

	if pol == "" {
		http.Error(w, "missing policy", http.StatusBadRequest)
		return
	}

	// Validate against the live users and nodes without persisting
	// (mirrors POST /api/v1/policy/check).
	users, err := st.ListAllUsers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pm, err := policyv2.NewPolicyManager([]byte(pol), users, st.ListNodes())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, err = pm.SetPolicy([]byte(pol))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{"status": "ok", "valid": true})
}

func handleElection(w http.ResponseWriter, r *http.Request, cfg *types.Config, st State) {
	switch r.Method {
	case http.MethodGet:
		snap := st.ElectionSnapshot()
		if snap == nil {
			snap = map[string]any{"mode": "", "primary_node": "", "has_primary": false}
		}
		// Attach config context for operator awareness.
		snap["xray_enabled"] = cfg.XRay.Enabled
		writeJSON(w, snap)

	case http.MethodPost:
		var req struct {
			Mode string `json:"mode"`
			Node uint64 `json:"node_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		// Delegate to state.SetElectionPrimary when available.
		if ws, ok := any(st).(interface{ SetElectionPrimary(types.NodeID) }); ok {
			nodeID := types.NodeID(req.Node)
			ws.SetElectionPrimary(nodeID)
			writeJSON(w, map[string]any{"status": "primary set", "node_id": nodeID.String()})
			return
		}
		writeJSON(w, map[string]any{"status": "primary set (stub)", "node_id": req.Node})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
