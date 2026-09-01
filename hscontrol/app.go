package hscontrol

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof" // nolint
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/davecgh/go-spew/spew"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/metrics"
	"github.com/pkg/profile"
	"github.com/rs/zerolog/log"
	"github.com/sasha-s/go-deadlock"
	apiv1 "github.com/tomiwebpro/stealthscale/hscontrol/api/v1"
	apiv2 "github.com/tomiwebpro/stealthscale/hscontrol/api/v2"
	"github.com/tomiwebpro/stealthscale/hscontrol/capver"
	"github.com/tomiwebpro/stealthscale/hscontrol/db"
	"github.com/tomiwebpro/stealthscale/hscontrol/stealth"
	"github.com/tomiwebpro/stealthscale/hscontrol/derp"
	derpServer "github.com/tomiwebpro/stealthscale/hscontrol/derp/server"
	"github.com/tomiwebpro/stealthscale/hscontrol/dns"
	"github.com/tomiwebpro/stealthscale/hscontrol/mapper"
	"github.com/tomiwebpro/stealthscale/hscontrol/state"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/types/change"
	"github.com/tomiwebpro/stealthscale/hscontrol/util"
	"github.com/tomiwebpro/stealthscale/hscontrol/webui"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/sync/errgroup"
	"tailscale.com/envknob"
	"tailscale.com/tailcfg"
	"tailscale.com/types/dnstype"
	"tailscale.com/types/key"
	"tailscale.com/util/dnsname"
)

var (
	errSTUNAddressNotSet                   = errors.New("STUN address not set")
	errUnsupportedLetsEncryptChallengeType = errors.New(
		"unknown value for Lets Encrypt challenge type",
	)
	errEmptyInitialDERPMap = errors.New(
		"initial DERPMap is empty, StealthScale requires at least one entry",
	)
)

var (
	debugDeadlock        = envknob.Bool("STEALTHSCALE_DEBUG_DEADLOCK")
	debugDeadlockTimeout = envknob.RegisterDuration("STEALTHSCALE_DEBUG_DEADLOCK_TIMEOUT")
)

func init() {
	deadlock.Opts.Disable = !debugDeadlock
	if debugDeadlock {
		deadlock.Opts.DeadlockTimeout = debugDeadlockTimeout()
		deadlock.Opts.PrintAllCurrentGoroutines = true
	}
}

const (
	updateInterval     = 5 * time.Second
	privateKeyFileMode = 0o600
	stealthscaleDirPerm   = 0o700
)

// StealthScale represents the base app of the service.
type StealthScale struct {
	cfg             *types.Config
	state           *state.State
	noisePrivateKey *key.MachinePrivate
	ephemeralGC     *db.EphemeralGarbageCollector

	DERPServer *derpServer.DERPServer

	// realIPMiddleware is nil when cfg.TrustedProxies is empty; the
	// router skips the mount and r.RemoteAddr stays as the TCP peer.
	realIPMiddleware func(http.Handler) http.Handler

	// Things that generate changes
	extraRecordMan *dns.ExtraRecordsMan
	authProvider   AuthProvider
	mapBatcher     *mapper.Batcher

	xrayServer *xray.Server

	clientStreamsOpen sync.WaitGroup
}

var (
	profilingEnabled = envknob.Bool("STEALTHSCALE_DEBUG_PROFILING_ENABLED")
	profilingPath    = envknob.String("STEALTHSCALE_DEBUG_PROFILING_PATH")
	tailsqlEnabled   = envknob.Bool("STEALTHSCALE_DEBUG_TAILSQL_ENABLED")
	tailsqlStateDir  = envknob.String("STEALTHSCALE_DEBUG_TAILSQL_STATE_DIR")
	tailsqlTSKey     = envknob.String("TS_AUTHKEY")
	dumpConfig       = envknob.Bool("STEALTHSCALE_DEBUG_DUMP_CONFIG")
)

func NewStealthScale(cfg *types.Config) (*StealthScale, error) {
	var err error

	if profilingEnabled {
		runtime.SetBlockProfileRate(1)
	}

	noisePrivateKey, err := readOrCreatePrivateKey(cfg.NoisePrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading or creating Noise protocol private key: %w", err)
	}

	s, err := state.NewState(cfg)
	if err != nil {
		return nil, fmt.Errorf("init state: %w", err)
	}

	app := StealthScale{
		cfg:               cfg,
		noisePrivateKey:   noisePrivateKey,
		clientStreamsOpen: sync.WaitGroup{},
		state:             s,
	}

	if len(cfg.TrustedProxies) > 0 {
		app.realIPMiddleware, err = trustedProxyRealIP(cfg.TrustedProxies)
		if err != nil {
			return nil, fmt.Errorf("building trusted_proxies middleware: %w", err)
		}
	}

	// Initialize ephemeral garbage collector
	ephemeralGC := db.NewEphemeralGarbageCollector(func(ni types.NodeID) {
		node, ok := app.state.GetNodeByID(ni)
		if !ok {
			log.Error().Uint64("node.id", ni.Uint64()).Msg("ephemeral node deletion failed")
			log.Debug().Caller().Uint64("node.id", ni.Uint64()).Msg("ephemeral node deletion failed because node not found in NodeStore")

			return
		}

		policyChanged, err := app.state.DeleteNode(node)
		if err != nil {
			log.Error().Err(err).EmbedObject(node).Msg("ephemeral node deletion failed")
			return
		}

		app.Change(policyChanged)
		log.Debug().Caller().EmbedObject(node).Msg("ephemeral node deleted because garbage collection timeout reached")
	})
	app.ephemeralGC = ephemeralGC

	var authProvider AuthProvider

	authProvider = NewAuthProviderWeb(cfg.ServerURL)
	if cfg.OIDC.Issuer != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		oidcProvider, err := NewAuthProviderOIDC(
			ctx,
			&app,
			cfg.ServerURL,
			&cfg.OIDC,
		)
		if err != nil {
			if cfg.OIDC.OnlyStartIfOIDCIsAvailable {
				return nil, err
			} else {
				log.Warn().Err(err).Msg("failed to set up OIDC provider, falling back to CLI based authentication")
			}
		} else {
			authProvider = oidcProvider
		}
	}

	app.authProvider = authProvider

	if app.cfg.TailcfgDNSConfig != nil && app.cfg.TailcfgDNSConfig.Proxied { // if MagicDNS
		// TODO(kradalby): revisit why this takes a list.
		var magicDNSDomains []dnsname.FQDN
		if cfg.PrefixV4 != nil {
			magicDNSDomains = append(
				magicDNSDomains,
				util.GenerateIPv4DNSRootDomain(*cfg.PrefixV4)...,
			)
		}

		if cfg.PrefixV6 != nil {
			magicDNSDomains = append(
				magicDNSDomains,
				util.GenerateIPv6DNSRootDomain(*cfg.PrefixV6)...,
			)
		}

		// we might have routes already from Split DNS
		if app.cfg.TailcfgDNSConfig.Routes == nil {
			app.cfg.TailcfgDNSConfig.Routes = make(map[string][]*dnstype.Resolver)
		}

		for _, d := range magicDNSDomains {
			// Empty non-nil slice rather than nil: tailcfg.DNSConfig.Clone
			// and dns.Config.Clone in tailscale drop map entries whose
			// value is nil (see tailscale.com/tailcfg/tailcfg_clone.go and
			// tailscale.com/net/dns/dns_clone.go: `if sv == nil { continue }`).
			// Sending nil here caused the client's wgengine LinkChange:major
			// handler to clobber /etc/resolv.conf on every tunnel-IP rebind
			// — the handler reapplies a Clone of lastDNSConfig and the magic
			// DNS routes vanish, taking the resolver with them for ~6 min
			// until the next route-changing netmap. Empty slice survives
			// Clone and carries the same "resolve locally" semantics
			// (tailscale.com/ipn/ipnlocal/node_backend.go:869 documents the
			// empty-resolver Routes form for Issue 2706).
			app.cfg.TailcfgDNSConfig.Routes[d.WithoutTrailingDot()] = []*dnstype.Resolver{}
		}
	}

	if cfg.DERP.ServerEnabled {
		derpServerKey, err := readOrCreatePrivateKey(cfg.DERP.ServerPrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("reading or creating DERP server private key: %w", err)
		}

		if derpServerKey.Equal(*noisePrivateKey) {
			return nil, fmt.Errorf(
				"DERP server private key and noise private key are the same: %w",
				err,
			)
		}

		if cfg.DERP.ServerVerifyClients {
			t := http.DefaultTransport.(*http.Transport) //nolint:forcetypeassert
			t.RegisterProtocol(
				derpServer.DerpVerifyScheme,
				derpServer.NewDERPVerifyTransport(app.handleVerifyRequest),
			)
		}

		embeddedDERPServer, err := derpServer.NewDERPServer(
			cfg.ServerURL,
			key.NodePrivate(*derpServerKey),
			&cfg.DERP,
		)
		if err != nil {
			return nil, err
		}

		app.DERPServer = embeddedDERPServer
	}

	return &app, nil
}

// Redirect to our TLS url.
func (h *StealthScale) redirect(w http.ResponseWriter, req *http.Request) {
	target := h.cfg.ServerURL + req.URL.RequestURI()
	http.Redirect(w, req, target, http.StatusFound) //nolint:gosec // G710: target prefixed by trusted ServerURL
}

func (h *StealthScale) scheduledTasks(ctx context.Context) {
	expireTicker := time.NewTicker(updateInterval)
	defer expireTicker.Stop()

	lastExpiryCheck := time.Unix(0, 0)

	var derpTickerChan <-chan time.Time

	if h.cfg.DERP.AutoUpdate && h.cfg.DERP.UpdateFrequency != 0 {
		derpTicker := time.NewTicker(h.cfg.DERP.UpdateFrequency)
		defer derpTicker.Stop()

		derpTickerChan = derpTicker.C
	}

	var extraRecordsUpdate <-chan []tailcfg.DNSRecord
	if h.extraRecordMan != nil {
		extraRecordsUpdate = h.extraRecordMan.UpdateCh()
	}

	var (
		haProber     *state.HAHealthProber
		haHealthChan <-chan time.Time
	)
	if h.cfg.Node.Routes.HA.ProbeInterval > 0 {
		haProber = state.NewHAHealthProber(
			h.state,
			h.cfg.Node.Routes.HA,
			h.cfg.ServerURL,
			h.mapBatcher.IsConnected,
		)

		haTicker := time.NewTicker(h.cfg.Node.Routes.HA.ProbeInterval)
		defer haTicker.Stop()

		haHealthChan = haTicker.C

		log.Info().
			Dur("interval", h.cfg.Node.Routes.HA.ProbeInterval).
			Dur("timeout", h.cfg.Node.Routes.HA.ProbeTimeout).
			Msg("HA subnet router health probing enabled")
	}

	var revokedKeyGCChan <-chan time.Time

	if h.cfg.PreAuthKeys.RevokedRetention > 0 {
		revokedKeyTicker := time.NewTicker(time.Hour)
		defer revokedKeyTicker.Stop()

		revokedKeyGCChan = revokedKeyTicker.C
	}

	// OAuth access tokens are short-lived (1h) and re-minted on demand; reap
	// expired rows hourly so the table stays bounded.
	accessTokenTicker := time.NewTicker(time.Hour)
	defer accessTokenTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Caller().Msg("scheduled task worker is shutting down.")
			return

		case <-revokedKeyGCChan:
			cutoff := time.Now().Add(-h.cfg.PreAuthKeys.RevokedRetention)

			reaped, err := h.state.DestroyRevokedPreAuthKeysBefore(cutoff)
			if err != nil {
				log.Error().Err(err).Msg("reaping revoked pre-auth keys")
			} else if reaped > 0 {
				log.Info().Int("count", reaped).Msg("reaped revoked pre-auth keys")
			}

		case <-accessTokenTicker.C:
			reaped, err := h.state.DeleteExpiredAccessTokens(time.Now())
			if err != nil {
				log.Error().Err(err).Msg("reaping expired oauth access tokens")
			} else if reaped > 0 {
				log.Debug().Int64("count", reaped).Msg("reaped expired oauth access tokens")
			}

		case <-expireTicker.C:
			var (
				expiredNodeChanges []change.Change
				changed            bool
			)

			lastExpiryCheck, expiredNodeChanges, changed = h.state.ExpireExpiredNodes(lastExpiryCheck)

			if changed {
				log.Trace().Interface("changes", expiredNodeChanges).Msgf("expiring nodes")

				// Send the changes directly since they're already in the new format
				for _, nodeChange := range expiredNodeChanges {
					h.Change(nodeChange)
				}
			}

		case <-derpTickerChan:
			log.Info().Msg("fetching DERPMap updates")

			derpMap, err := backoff.Retry(ctx, func() (*tailcfg.DERPMap, error) { //nolint:contextcheck
				derpMap, err := derp.GetDERPMap(h.cfg.DERP)
				if err != nil {
					return nil, err
				}

				if h.cfg.DERP.ServerEnabled && h.cfg.DERP.AutomaticallyAddEmbeddedDerpRegion {
					region, _ := h.DERPServer.GenerateRegion()
					derpMap.Regions[region.RegionID] = &region
				}

				return derpMap, nil
			}, backoff.WithBackOff(backoff.NewExponentialBackOff()))
			if err != nil {
				log.Error().Err(err).Msg("failed to build new DERPMap, retrying later")
				continue
			}

			h.state.SetDERPMap(derpMap)

			h.Change(change.DERPMap())

		case records, ok := <-extraRecordsUpdate:
			if !ok {
				continue
			}

			h.cfg.SetExtraRecords(records)

			h.Change(change.ExtraRecords())

		case <-haHealthChan:
			haProber.ProbeOnce(ctx, h.Change)
		}
	}
}

// controlPlanePaths are the Tailscale/Headscale control-protocol endpoints.
// We deliberately do NOT emit security headers on these: the extra
// X-Frame-Options / CSP / X-Content-Type-Options headers are themselves a
// fingerprint that distinguishes this server from a benign website, and the
// control protocol is machine-to-machine and needs no browser hardening.
// Note: exact-match previously missed real paths like /api/v1/node,
// /api/v2/device/{id} and caused API responses to carry browser headers
// (fingerprintable vs decoy). Now we use prefix matching for /api/*.
var controlPlanePaths = map[string]bool{
	ts2021UpgradePath:       true,
	"/key":                  true,
	"/derp":                 true,
	"/derp/probe":           true,
	"/derp/latency-check":   true,
	"/bootstrap-dns":        true,
	"/machine/ping-response": true,
	"/health":               true,
	"/version":              true,
	"/register":             true,
	"/auth":                 true,
	"/oidc/callback":        true,
	"/apple":                true,
	"/windows":              true,
	"/verify":               true,
	"/api":                  true,
	"/robots.txt":           true,
}

func isControlPlanePath(p string) bool {
	if controlPlanePaths[p] {
		return true
	}
	// Prefix checks for API and other control paths that have sub-resources
	if strings.HasPrefix(p, "/api/") || p == "/api" {
		return true
	}
	if strings.HasPrefix(p, "/register/") || strings.HasPrefix(p, "/auth/") {
		return true
	}
	if strings.HasPrefix(p, "/apple/") || strings.HasPrefix(p, "/windows/") {
		return true
	}
	if p == "/ts2021" || strings.HasPrefix(p, "/ts2021/") {
		return true
	}
	return false
}

// securityHeaders sets baseline response headers on HTML/UI responses:
// deny framing (clickjacking), forbid MIME-type sniffing, drop the Referer
// header on outbound navigation. Cheap defense-in-depth for human-facing
// surfaces. It is intentionally skipped on the machine control-protocol paths
// so those do not advertise a browser-oriented header set (a fingerprint).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isControlPlanePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// gateDERPOnStealth wraps a DERP handler so it only answers while the stealth
// transport is satisfied; otherwise it refuses (fail-closed) to avoid leaking
// the fingerprintable DERP protocol on the public control port when stealth is
// not actually serving. Returns 404 (not 421) to avoid distinctive error
// that distinguishes stealth failure from decoy's 404.
func (h *StealthScale) gateDERPOnStealth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !derp.ShouldIncludeDERP(h.cfg) {
			IncDERPGated()
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveHumaMux dispatches to a Huma mux mounted under the outer chi router.
// Huma registers operations at absolute paths (/api/v1/...), so chi's route
// context must be cleared for the inner mux to re-match against the original URL.
func serveHumaMux(mux http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		mux.ServeHTTP(w, req.WithContext(
			context.WithValue(req.Context(), chi.RouteCtxKey, nil),
		))
	}
}

func (h *StealthScale) createRouter(apiV1Mux, apiV2Mux http.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(metrics.Collector(metrics.CollectorOpts{
		Host:  false,
		Proto: true,
		Skip: func(r *http.Request) bool {
			return r.Method != http.MethodOptions
		},
	}))
	r.Use(middleware.RequestID)

	if h.realIPMiddleware != nil {
		r.Use(h.realIPMiddleware)
	}

	r.Use(middleware.RequestLogger(&zerologRequestLogger{}))
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)

	// TS2021 accepts both the native client's HTTP POST upgrade and the
	// browser/WASM client's WebSocket GET upgrade; NoiseUpgradeHandler
	// dispatches on the Upgrade header, not the method. Registering GET as
	// well keeps the router from rejecting the WebSocket handshake with 405.
	//
	// When stealth control enforcement is enabled the legacy Noise registration
	// endpoint is removed from the public router so nodes can only register
	// over the stealth VLESS transport (fail-closed for the control plane).
	stealthControlOnly := h.cfg.XRay.Enabled && h.cfg.XRay.Stealth.EnforceControl
	if !stealthControlOnly {
		r.Get(ts2021UpgradePath, h.NoiseUpgradeHandler)
		r.Post(ts2021UpgradePath, h.NoiseUpgradeHandler)
	} else {
		log.Warn().
			Msg("stealth: legacy /ts2021 Noise registration disabled (xray.stealth.enforce_control); nodes must register over VLESS")
	}

	r.Get("/robots.txt", h.RobotsHandler)
	r.Get("/health", h.HealthHandler)
	r.Get("/version", h.VersionHandler)
	r.Get("/key", h.KeyHandler)
	r.Get("/register/{auth_id}", h.authProvider.RegisterHandler)
	r.Get("/auth/{auth_id}", h.authProvider.AuthHandler)

	if provider, ok := h.authProvider.(*AuthProviderOIDC); ok {
		r.Get("/oidc/callback", provider.OIDCCallbackHandler)
		r.Post("/register/confirm/{auth_id}", provider.RegisterConfirmHandler)
	}

	r.Get("/apple", h.AppleConfigMessage)
	r.Get("/apple/{platform}", h.ApplePlatformConfig)
	r.Get("/windows", h.WindowsConfigMessage)

	r.Post("/verify", h.VerifyHandler)

	if h.cfg.DERP.ServerEnabled {
		// When stealth enforcement is on, the relay is only reachable while the
		// stealth transport is actually serving (fail-closed). This prevents the
		// stock Tailscale DERP protocol from being exposed on the public control
		// port if the stealth transport is down.
		var derpHandler http.Handler = http.HandlerFunc(h.DERPServer.DERPHandler)
		var derpProbe http.Handler = http.HandlerFunc(derpServer.DERPProbeHandler)
		var derpLatency http.Handler = http.HandlerFunc(derpServer.DERPProbeHandler)
		var derpBootstrap http.Handler = http.HandlerFunc(
			derpServer.DERPBootstrapDNSHandler(h.state.DERPMap()),
		)
		if h.cfg.XRay.Stealth.Enforce {
			gate := h.gateDERPOnStealth
			derpHandler = gate(derpHandler)
			derpProbe = gate(derpProbe)
			derpLatency = gate(derpLatency)
			derpBootstrap = gate(derpBootstrap)
		}
		r.Handle("/derp", derpHandler)
		r.Handle("/derp/probe", derpProbe)
		r.Handle("/derp/latency-check", derpLatency)
		r.Handle("/bootstrap-dns", derpBootstrap)
	}

	// Auth is enforced inside each Huma mux per-operation, so the whole API
	// mounts as one handler per version: operations need an API key while the
	// OpenAPI document and docs UI stay public. v1 is the stealthscale-native admin
	// API; v2 is StealthScale's v2 API, which ports some endpoints from Tailscale.
	r.Route("/api", func(r chi.Router) {
		r.Handle("/v1/*", serveHumaMux(apiV1Mux))
		r.Handle("/v2/*", serveHumaMux(apiV2Mux))
	})
	// Ping response endpoint: receives HEAD from clients responding
	// to a [tailcfg.PingRequest]. The unguessable ping ID serves as authentication.
	r.Head("/machine/ping-response", h.PingResponseHandler)

	r.Get("/favicon.ico", FaviconHandler)
	r.Get("/", BlankHandler)

	// Embedded WebUI (headscale-ui style) — same codebase for server and client.
	// Served at /web and /admin (alias).
	webui.Register(r, h.cfg, h.state)

	return r
}

// Serve launches the HTTP servers that run StealthScale and its API.
//
//nolint:gocyclo // complex server startup function
func (h *StealthScale) Serve() error {
	var err error

	capver.CanOldCodeBeCleanedUp()

	if profilingEnabled {
		if profilingPath != "" {
			err = os.MkdirAll(profilingPath, os.ModePerm)
			if err != nil {
				log.Fatal().Err(err).Msg("failed to create profiling directory")
			}

			defer profile.Start(profile.ProfilePath(profilingPath)).Stop()
		} else {
			defer profile.Start().Stop()
		}
	}

	if dumpConfig {
		// Redact secrets before dumping — previous unsanitized Dump leaked
		// XRay.Secret, Reality.PrivateKey, OIDC.ClientSecret, DB/Postgres.Pass
		// to stderr/control_logs via env STEALTHSCALE_DEBUG_DUMP_CONFIG.
		cfgCopy := *h.cfg
		cfgCopy.XRay.Secret = "<redacted>"
		cfgCopy.XRay.Reality.PrivateKey = "<redacted>"
		cfgCopy.XRay.Reality.PublicKey = "<redacted>"
		cfgCopy.OIDC.ClientSecret = "<redacted>"
		cfgCopy.Database.Postgres.Pass = "<redacted>"
		cfgCopy.CLI.APIKey = "<redacted>"
		// Also redact raw viper values that may contain secrets
		spew.Dump(cfgCopy)
		log.Warn().Msg("STEALTHSCALE_DEBUG_DUMP_CONFIG is enabled — config dumped with secrets redacted; unset in production")
	}

	versionInfo := types.GetVersionInfo()
	log.Info().Str("version", versionInfo.Version).Str("commit", versionInfo.Commit).Msg("coordination server starting")
	log.Info().
		Str("minimum_version", capver.TailscaleVersion(capver.MinSupportedCapabilityVersion)).
		Msg("Clients with a lower minimum version will be rejected")

	h.mapBatcher = mapper.NewBatcherAndMapper(h.cfg, h.state)

	h.mapBatcher.Start()
	defer h.mapBatcher.Close()

	if h.cfg.DERP.ServerEnabled {
		// When embedded DERP is enabled we always need a STUN server
		if h.cfg.DERP.STUNAddr == "" {
			return errSTUNAddressNotSet
		}

		go h.DERPServer.ServeSTUN()
	}

	derpMap, err := derp.GetDERPMap(h.cfg.DERP)
	if err != nil {
		return fmt.Errorf("getting DERPMap: %w", err)
	}

	if h.cfg.DERP.ServerEnabled && h.cfg.DERP.AutomaticallyAddEmbeddedDerpRegion {
		region, _ := h.DERPServer.GenerateRegion()
		derpMap.Regions[region.RegionID] = &region
	}

	if len(derpMap.Regions) == 0 {
		return errEmptyInitialDERPMap
	}

	h.state.SetDERPMap(derpMap)

	// Start ephemeral node garbage collector and schedule all nodes
	// that are already in the database and ephemeral. If they are still
	// around between restarts, they will reconnect and the GC will
	// be cancelled.
	go h.ephemeralGC.Start()

	ephmNodes := h.state.ListEphemeralNodes()
	for _, node := range ephmNodes.All() {
		h.ephemeralGC.Schedule(node.ID(), h.cfg.Node.Ephemeral.InactivityTimeout)
	}

	if h.cfg.DNSConfig.ExtraRecordsPath != "" {
		h.extraRecordMan, err = dns.NewExtraRecordsManager(h.cfg.DNSConfig.ExtraRecordsPath)
		if err != nil {
			return fmt.Errorf("setting up extrarecord manager: %w", err)
		}

		h.cfg.SetExtraRecords(h.extraRecordMan.Records())

		go h.extraRecordMan.Run()
		defer h.extraRecordMan.Close()
	}

	// Start all scheduled tasks, e.g. expiring nodes, derp updates and
	// records updates
	scheduleCtx, scheduleCancel := context.WithCancel(context.Background())
	defer scheduleCancel()

	go h.scheduledTasks(scheduleCtx)

	// Prepare group for running listeners
	errorGroup := new(errgroup.Group)

	ctx := context.Background()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register the runtime stealth checker so DERP fallback is gated on the
	// stealth transport actually serving (fail-closed). Only enforce when
	// stealth enforcement is enabled.
	if h.cfg.XRay.Enabled && h.cfg.XRay.Stealth.Enforce {
		derp.SetStealthChecker(stealth.New(&h.cfg.XRay))
		defer derp.SetStealthChecker(nil)
	}
	SetStealthReady(false)

	if err := h.StartXRayServer(ctx); err != nil {
		return fmt.Errorf("starting xray server: %w", err)
	}
	// The stealth transport is now serving; lift the fail-closed DERP gate.
	derp.MarkStealthReady()
	SetStealthReady(true)

	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), types.HTTPTimeout)
		defer shutdownCancel()
		h.StopXRayServer(shutdownCtx)
		SetStealthReady(false)
	}()

	//
	//
	// Set up LOCAL listeners
	//

	socketListener, err := h.listenSocket(context.Background())
	if err != nil {
		return fmt.Errorf("setting up socket: %w", err)
	}

	// The Huma v1 API mux matches full /api/v1/... paths and is shared by
	// the local unix socket (served without authentication, local trust)
	// and the remote TCP router (served behind the API-key middleware).
	humaMux, _ := apiv1.Handler(apiv1.Backend{
		State:  h.state,
		Change: h.Change,
		Cfg:    h.cfg,
	})

	// The StealthScale v2 API. Served behind Basic/Bearer auth on the remote
	// listener, and over the local unix socket (local trust) so the CLI can
	// manage OAuth clients through the same v2 keys handler the Tailscale
	// ecosystem uses.
	humaV2Mux, _ := apiv2.Handler(apiv2.Backend{
		State:  h.state,
		Change: h.Change,
		Cfg:    h.cfg,
	})

	// Serve both Huma APIs over the unix socket without TLS or auth: socket
	// access implies trust. WithLocalTrust marks these requests so each API's
	// security middleware skips the credential check. v2 paths route to the v2
	// mux; everything else (the v1 paths) to v1.
	socketHandler := http.NewServeMux()
	socketHandler.Handle("/api/v2/", apiv2.WithLocalTrust(humaV2Mux))
	socketHandler.Handle("/", apiv1.WithLocalTrust(humaMux))

	socketServer := &http.Server{
		Handler:     socketHandler,
		ReadTimeout: types.HTTPTimeout,
	}

	errorGroup.Go(func() error { return socketServer.Serve(socketListener) })

	//
	//
	// Set up REMOTE listeners
	//

	tlsConfig, err := h.getTLSSettings()
	if err != nil {
		return fmt.Errorf("configuring TLS settings: %w", err)
	}

	//
	//
	// HTTP setup
	//
	// This is the regular router that we expose
	// over our main Addr
	router := h.createRouter(humaMux, humaV2Mux)

	httpServer := &http.Server{
		Addr:        h.cfg.Addr,
		Handler:     router,
		ReadTimeout: types.HTTPTimeout,

		// Long polling should not have any timeout, this is overridden
		// further down the chain
		WriteTimeout: types.HTTPTimeout,
	}

	var httpListener net.Listener

	if tlsConfig != nil {
		httpServer.TLSConfig = tlsConfig
		httpListener, err = tls.Listen("tcp", h.cfg.Addr, tlsConfig)
	} else {
		httpListener, err = new(net.ListenConfig).Listen(context.Background(), "tcp", h.cfg.Addr)
	}

	if err != nil {
		return fmt.Errorf("binding to TCP address: %w", err)
	}

	errorGroup.Go(func() error { return httpServer.Serve(httpListener) })

	log.Info().
		Msgf("listening and serving HTTP on: %s", h.cfg.Addr)

	// Only start debug/metrics server if address is configured
	var debugHTTPServer *http.Server

	var debugHTTPListener net.Listener

	if h.cfg.MetricsAddr != "" {
		debugHTTPListener, err = (&net.ListenConfig{}).Listen(ctx, "tcp", h.cfg.MetricsAddr)
		if err != nil {
			return fmt.Errorf("binding to TCP address: %w", err)
		}

		debugHTTPServer = h.debugHTTPServer()

		errorGroup.Go(func() error { return debugHTTPServer.Serve(debugHTTPListener) })

		log.Info().
			Msgf("listening and serving debug and metrics on: %s", h.cfg.MetricsAddr)
	} else {
		log.Info().Msg("metrics server disabled (metrics_listen_addr is empty)")
	}

	var tailsqlContext context.Context

	if tailsqlEnabled {
		if h.cfg.Database.Type != types.DatabaseSqlite {
			//nolint:gocritic // exitAfterDefer: Fatal exits during initialization before servers start
			log.Fatal().
				Str("type", h.cfg.Database.Type).
				Msgf("tailsql only support %q", types.DatabaseSqlite)
		}

		if tailsqlTSKey == "" {
			//nolint:gocritic // exitAfterDefer: Fatal exits during initialization before servers start
			log.Fatal().Msg("tailsql requires TS_AUTHKEY to be set")
		}

		tailsqlContext = context.Background()

		go runTailSQLService(ctx, util.TSLogfWrapper(), tailsqlStateDir, h.cfg.Database.Sqlite.Path) //nolint:errcheck
	}

	// Handle common process-killing signals so we can gracefully shut down:
	sigc := make(chan os.Signal, 1)
	setupSignalNotify(sigc)

	sigFunc := func(c chan os.Signal) {
		// Wait for a SIGINT or SIGKILL:
		for {
			sig := <-c
			if isSIGHUP(sig) {
				log.Info().
					Str("signal", sig.String()).
					Msg("Received SIGHUP, reloading ACL policy")

				if h.cfg.Policy.IsEmpty() {
					continue
				}

				changes, err := h.state.ReloadPolicy()
				if err != nil {
					log.Error().Err(err).Msgf("reloading policy")
					continue
				}

				h.Change(changes...)

			} else {
				info := func(msg string) { log.Info().Msg(msg) }

				log.Info().
					Str("signal", sig.String()).
					Msg("Received signal to stop, shutting down gracefully")

				scheduleCancel()
				h.ephemeralGC.Close()

				// Gracefully shut down servers
				shutdownCtx, cancel := context.WithTimeout(
					context.WithoutCancel(ctx),
					types.HTTPShutdownTimeout,
				)
				defer cancel()

				if debugHTTPServer != nil {
					info("shutting down debug http server")

					err := debugHTTPServer.Shutdown(shutdownCtx)
					if err != nil {
						log.Error().Err(err).Msg("failed to shutdown prometheus http")
					}
				}

				info("shutting down main http server")

				err := httpServer.Shutdown(shutdownCtx)
				if err != nil {
					log.Error().Err(err).Msg("failed to shutdown http")
				}

				info("closing batcher")
				h.mapBatcher.Close()

				info("waiting for netmap stream to close")
				h.clientStreamsOpen.Wait()

				info("shutting down api server (socket)")

				if err := socketServer.Shutdown(shutdownCtx); err != nil { //nolint:noinlineerr
					log.Error().Err(err).Msg("failed to shutdown socket server")
				}

				if tailsqlContext != nil {
					info("shutting down tailsql")
					tailsqlContext.Done()
				}

				// Close network listeners
				info("closing network listeners")

				if debugHTTPListener != nil {
					debugHTTPListener.Close()
				}

				httpListener.Close()

				// Stop listening (and unlink the socket if unix type):
				info("closing socket listener")
				socketListener.Close()

				// Close state connections
				info("closing state and database")

				err = h.state.Close()
				if err != nil {
					log.Error().Err(err).Msg("failed to close state")
				}

				log.Info().
					Msg("coordination server stopped")

				return
			}
		}
	}

	errorGroup.Go(func() error {
		sigFunc(sigc)

		return nil
	})

	return errorGroup.Wait()
}

func (h *StealthScale) getTLSSettings() (*tls.Config, error) {
	tlsEnabled := h.cfg.TLS.LetsEncrypt.Hostname != "" || h.cfg.TLS.CertPath != ""
	if tlsEnabled && !strings.HasPrefix(h.cfg.ServerURL, "https://") {
		log.Warn().Msg("listening with TLS but ServerURL does not start with https://")
	} else if !tlsEnabled && !strings.HasPrefix(h.cfg.ServerURL, "http://") {
		log.Warn().Msg("listening without TLS but ServerURL does not start with http://")
	}

	if h.cfg.TLS.LetsEncrypt.Hostname != "" {
		certManager := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(h.cfg.TLS.LetsEncrypt.Hostname),
			Cache:      autocert.DirCache(h.cfg.TLS.LetsEncrypt.CacheDir),
			Client: &acme.Client{
				DirectoryURL: h.cfg.ACMEURL,
				HTTPClient: &http.Client{
					Transport: &acmeLogger{
						rt: http.DefaultTransport,
					},
				},
			},
			Email: h.cfg.ACMEEmail,
		}

		switch h.cfg.TLS.LetsEncrypt.ChallengeType {
		case types.TLSALPN01ChallengeType:
			// Configuration via autocert with TLS-ALPN-01 (https://tools.ietf.org/html/rfc8737)
			// The RFC requires that the validation is done on port 443; in other words, stealthscale
			// must be reachable on port 443.
			return certManager.TLSConfig(), nil

		case types.HTTP01ChallengeType:
			// Configuration via autocert with HTTP-01. This requires listening on
			// port 80 for the certificate validation in addition to the stealthscale
			// service, which can be configured to run on any other port.
			server := &http.Server{
				Addr:        h.cfg.TLS.LetsEncrypt.Listen,
				Handler:     certManager.HTTPHandler(http.HandlerFunc(h.redirect)),
				ReadTimeout: types.HTTPTimeout,
			}

			go func() {
				err := server.ListenAndServe()
				log.Fatal().
					Caller().
					Err(err).
					Msg("failed to set up a HTTP server")
			}()

			return certManager.TLSConfig(), nil

		default:
			return nil, errUnsupportedLetsEncryptChallengeType
		}
	}

	if h.cfg.TLS.CertPath == "" {
		return nil, nil //nolint:nilnil // intentional: no TLS config when neither LetsEncrypt nor a cert path is set
	}

	tlsConfig := &tls.Config{
		NextProtos:   []string{"http/1.1"},
		Certificates: make([]tls.Certificate, 1),
		MinVersion:   tls.VersionTLS12,
	}

	cert, err := tls.LoadX509KeyPair(h.cfg.TLS.CertPath, h.cfg.TLS.KeyPath)
	if err != nil {
		return nil, err
	}

	tlsConfig.Certificates[0] = cert

	return tlsConfig, nil
}

func readOrCreatePrivateKey(path string) (*key.MachinePrivate, error) {
	dir := filepath.Dir(path)

	err := util.EnsureDir(dir)
	if err != nil {
		return nil, fmt.Errorf("ensuring private key directory: %w", err)
	}

	privateKey, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		log.Info().Str("path", path).Msg("no private key file at path, creating...")

		machineKey := key.NewMachine()

		machineKeyStr, err := machineKey.MarshalText()
		if err != nil {
			return nil, fmt.Errorf(
				"converting private key to string for saving: %w",
				err,
			)
		}

		err = os.WriteFile(path, machineKeyStr, privateKeyFileMode)
		if err != nil {
			return nil, fmt.Errorf(
				"saving private key to disk at path %q: %w",
				path,
				err,
			)
		}

		return &machineKey, nil
	} else if err != nil {
		return nil, fmt.Errorf("reading private key file: %w", err)
	}

	trimmedPrivateKey := strings.TrimSpace(string(privateKey))

	var machineKey key.MachinePrivate
	if err = machineKey.UnmarshalText([]byte(trimmedPrivateKey)); err != nil { //nolint:noinlineerr
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	return &machineKey, nil
}

// Change is used to send changes to nodes.
// All change should be enqueued here and empty will be automatically
// ignored.
func (h *StealthScale) Change(cs ...change.Change) {
	h.mapBatcher.AddWork(cs...)
}

// HTTPHandler returns an [http.Handler] for the [StealthScale] control server.
// The handler serves the Tailscale control protocol including the /key
// endpoint and /ts2021 Noise upgrade path.
func (h *StealthScale) HTTPHandler() http.Handler {
	humaMux, _ := apiv1.Handler(apiv1.Backend{
		State:  h.state,
		Change: h.Change,
		Cfg:    h.cfg,
	})

	humaV2Mux, _ := apiv2.Handler(apiv2.Backend{
		State:  h.state,
		Change: h.Change,
		Cfg:    h.cfg,
	})

	return h.createRouter(humaMux, humaV2Mux)
}

// NoisePublicKey returns the server's Noise protocol public key.
func (h *StealthScale) NoisePublicKey() key.MachinePublic {
	return h.noisePrivateKey.Public()
}

// StopXRayServer shuts down all VLESS transport listeners, if any are
// running. It is a no-op when the xray transport was never started.
func (h *StealthScale) StopXRayServer(ctx context.Context) {
	if h.xrayServer != nil {
		_ = h.xrayServer.Shutdown(ctx)
	}
}

// GetState returns the server's state manager for programmatic access
// to users, nodes, policies, and other server state.
func (h *StealthScale) GetState() *state.State {
	return h.state
}

// GetConfig returns the server's configuration.
func (h *StealthScale) GetConfig() *types.Config {
	return h.cfg
}

// SetServerURLForTest updates the server URL in the configuration.
// This is needed for test servers where the URL is not known until
// the HTTP test server starts.
// It panics when called outside of tests.
func (h *StealthScale) SetServerURLForTest(tb testing.TB, url string) {
	tb.Helper()

	h.cfg.ServerURL = url
}

// StartBatcherForTest initialises and starts the map response batcher.
// It registers a cleanup function on tb to stop the batcher.
// It panics when called outside of tests.
func (h *StealthScale) StartBatcherForTest(tb testing.TB) {
	tb.Helper()

	h.mapBatcher = mapper.NewBatcherAndMapper(h.cfg, h.state)
	h.mapBatcher.Start()
	tb.Cleanup(func() { h.mapBatcher.Close() })
}

// MapBatcher returns the map response batcher (for test use).
func (h *StealthScale) MapBatcher() *mapper.Batcher {
	return h.mapBatcher
}

// StartEphemeralGCForTest starts the ephemeral node garbage collector.
// It registers a cleanup function on tb to stop the collector.
// It panics when called outside of tests.
func (h *StealthScale) StartEphemeralGCForTest(tb testing.TB) {
	tb.Helper()

	go h.ephemeralGC.Start()

	tb.Cleanup(func() { h.ephemeralGC.Close() })
}

// Provide some middleware that can inspect the ACME/autocert https calls
// and log when things are failing.
type acmeLogger struct {
	rt http.RoundTripper
}

// RoundTrip will log when ACME/autocert failures happen either when err != nil OR
// when http status codes indicate a failure has occurred.
func (l *acmeLogger) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := l.rt.RoundTrip(req)
	if err != nil {
		log.Error().Err(err).Str("url", req.URL.String()).Msg("acme request failed")
		return nil, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		log.Error().Int("status_code", resp.StatusCode).Str("url", req.URL.String()).Bytes("body", body).Msg("acme request returned error")
	}

	return resp, nil
}

// [zerologRequestLogger] implements chi's [middleware.LogFormatter]
// to route HTTP request logs through zerolog.
type zerologRequestLogger struct{}

func (z *zerologRequestLogger) NewLogEntry(
	r *http.Request,
) middleware.LogEntry {
	return &zerologLogEntry{
		method: r.Method,
		path:   r.URL.Path,
		proto:  r.Proto,
		remote: r.RemoteAddr,
	}
}

type zerologLogEntry struct {
	method string
	path   string
	proto  string
	remote string
}

func (e *zerologLogEntry) Write(
	status, bytes int,
	header http.Header,
	elapsed time.Duration,
	extra any,
) {
	log.Info().
		Str("method", e.method).
		Str("path", e.path).
		Str("proto", e.proto).
		Str("remote", e.remote).
		Int("status", status).
		Int("bytes", bytes).
		Dur("elapsed", elapsed).
		Msg("http request")
}

func (e *zerologLogEntry) Panic(
	v any,
	stack []byte,
) {
	log.Error().
		Interface("panic", v).
		Bytes("stack", stack).
		Msg("http handler panic")
}
