package hsic

import "github.com/tomiwebpro/stealthscale/hscontrol/types"

func MinimumConfigYAML() string {
	return `
private_key_path: /tmp/private.key
noise:
  private_key_path: /tmp/noise_private.key
`
}

func DefaultConfigEnv() map[string]string {
	return map[string]string{
		"STEALTHSCALE_LOG_LEVEL":                         "trace",
		"STEALTHSCALE_POLICY_PATH":                       "",
		"STEALTHSCALE_DATABASE_TYPE":                     "sqlite",
		"STEALTHSCALE_DATABASE_SQLITE_PATH":              "/tmp/integration_test_db.sqlite3",
		"STEALTHSCALE_DATABASE_DEBUG":                    "0",
		"STEALTHSCALE_DATABASE_GORM_SLOW_THRESHOLD":      "1",
		"STEALTHSCALE_EPHEMERAL_NODE_INACTIVITY_TIMEOUT": "30m",
		"STEALTHSCALE_PREFIXES_V4":                       "100.64.0.0/10",
		"STEALTHSCALE_PREFIXES_V6":                       "fd7a:115c:a1e0::/48",
		"STEALTHSCALE_DNS_BASE_DOMAIN":                   "stealthscale.net",
		"STEALTHSCALE_DNS_MAGIC_DNS":                     "true",
		"STEALTHSCALE_DNS_OVERRIDE_LOCAL_DNS":            "false",
		"STEALTHSCALE_DNS_NAMESERVERS_GLOBAL":            "127.0.0.11 1.1.1.1",
		"STEALTHSCALE_PRIVATE_KEY_PATH":                  "/tmp/private.key",
		"STEALTHSCALE_NOISE_PRIVATE_KEY_PATH":            "/tmp/noise_private.key",
		"STEALTHSCALE_METRICS_LISTEN_ADDR":               "0.0.0.0:9090",
		"STEALTHSCALE_DEBUG_PORT":                        "40000",

		// Embedded DERP is the default for test isolation.
		// Tests should not depend on external DERP infrastructure.
		// Use [WithPublicDERP] to opt out for tests that explicitly
		// need public DERP relays.
		"STEALTHSCALE_DERP_URLS":                    "",
		"STEALTHSCALE_DERP_AUTO_UPDATE_ENABLED":     "false",
		"STEALTHSCALE_DERP_UPDATE_FREQUENCY":        "1m",
		"STEALTHSCALE_DERP_SERVER_ENABLED":          "true",
		"STEALTHSCALE_DERP_SERVER_REGION_ID":        "999",
		"STEALTHSCALE_DERP_SERVER_REGION_CODE":      binStealthScale,
		"STEALTHSCALE_DERP_SERVER_REGION_NAME":      "StealthScale Embedded DERP",
		"STEALTHSCALE_DERP_SERVER_STUN_LISTEN_ADDR": "0.0.0.0:3478",
		"STEALTHSCALE_DERP_SERVER_PRIVATE_KEY_PATH": "/tmp/derp.key",
		"DERP_DEBUG_LOGS":                        "true",
		"DERP_PROBER_DEBUG_LOGS":                 "true",

		// a bunch of tests (ACL/Policy) rely on predictable IP alloc,
		// so ensure the sequential alloc is used by default.
		"STEALTHSCALE_PREFIXES_ALLOCATION": string(types.IPAllocationStrategySequential),
	}
}
