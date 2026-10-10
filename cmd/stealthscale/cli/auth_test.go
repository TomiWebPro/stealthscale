package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// testAuthID is a syntactically valid auth ID
// (the hskey-authreq- prefix plus 24 hex chars
// produced by types.NewAuthID).
const testAuthID = "hskey-authreq-0123456789abcdef01234567"

// setAuthFlag sets a flag on a command and restores
// its previous value when the test ends, so flag
// state cannot leak between tests (the commands
// are package globals).
func setAuthFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()

	flag := cmd.Flags().Lookup(name)
	require.NotNil(t, flag, "%s command has no --%s flag", cmd.Name(), name)

	old := flag.Value.String()
	require.NoError(t, flag.Value.Set(value))

	t.Cleanup(func() {
		require.NoError(t, flag.Value.Set(old))
	})
}

// resetAuthFlags returns a command's auth input
// flags to their defaults, so values parsed by a
// previous Execute do not leak into the next
// subtest (cobra does not reset parsed flags).
func resetAuthFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()

	for _, name := range []string{"auth-id", "url"} {
		setAuthFlag(t, cmd, name, "")
	}
}

// setViperCLIConfig points the CLI's API client at the
// test server and restores the previous viper values
// when the test ends.
func setViperCLIConfig(t *testing.T, address string) {
	t.Helper()

	oldAddress := viper.Get("cli.address")
	oldAPIKey := viper.Get("cli.api_key")

	viper.Set("cli.address", address)
	viper.Set("cli.api_key", "test-api-key")

	t.Cleanup(func() {
		viper.Set("cli.address", oldAddress)
		viper.Set("cli.api_key", oldAPIKey)
	})
}

// authAPIFixture serves the v1 auth endpoints like
// the coordinator (POST /api/v1/auth/{approve,reject,register}),
// decoding the request body's authId (and user for
// register) into the pointers, and answers 200 with
// an empty JSON object. called tracks whether any
// request arrived.
func authAPIFixture(
	t *testing.T,
	called *atomic.Bool,
	gotAuthID, gotUser *string,
) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)

		var body struct {
			AuthID *string `json:"authId"`
			User   *string `json:"user"`
		}

		_ = json.NewDecoder(r.Body).Decode(&body)

		if body.AuthID != nil {
			*gotAuthID = *body.AuthID
		}

		if body.User != nil {
			*gotUser = *body.User
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// TestParseAuthID pins the accepted input shapes: a
// bare auth ID, or a full auth-flow URL whose path is
// /auth/<id> or /register/<id> — the only handles on
// a pending session. Anything else must be an error
// so a command never silently targets a different
// session.
func TestParseAuthID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    types.AuthID
		wantErr bool
	}{
		{
			name:  "bare auth ID",
			input: testAuthID,
			want:  types.AuthID(testAuthID),
		},
		{
			name:  "full https auth URL",
			input: "https://coordinator.example/auth/" + testAuthID,
			want:  types.AuthID(testAuthID),
		},
		{
			name:  "full http register URL with port",
			input: "http://localhost:8080/register/" + testAuthID,
			want:  types.AuthID(testAuthID),
		},
		{
			name:  "path-only auth URL",
			input: "/auth/" + testAuthID,
			want:  types.AuthID(testAuthID),
		},
		{
			name:  "path-only register URL",
			input: "/register/" + testAuthID,
			want:  types.AuthID(testAuthID),
		},
		{
			name:  "auth URL with query string",
			input: "https://coordinator.example/auth/" + testAuthID + "?state=held",
			want:  types.AuthID(testAuthID),
		},
		{
			name:  "auth URL with trailing slash",
			input: "https://coordinator.example/auth/" + testAuthID + "/",
			want:  types.AuthID(testAuthID),
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "garbage bare value",
			input:   "not-an-auth-id",
			wantErr: true,
		},
		{
			name:    "bare value with wrong prefix",
			input:   "hskey-node-0123456789abcdef01234567",
			wantErr: true,
		},
		{
			name:    "URL with unknown path kind",
			input:   "https://coordinator.example/other/" + testAuthID,
			wantErr: true,
		},
		{
			name:    "URL with single path segment",
			input:   "https://coordinator.example/auth",
			wantErr: true,
		},
		{
			name:    "URL with no path",
			input:   "https://coordinator.example",
			wantErr: true,
		},
		{
			name:    "auth URL with malformed ID",
			input:   "https://coordinator.example/auth/not-an-auth-id",
			wantErr: true,
		},
		{
			name:    "auth URL with truncated ID",
			input:   "https://coordinator.example/auth/" + testAuthID[:len(testAuthID)-1],
			wantErr: true,
		},
		{
			name:    "auth URL with injected path segment",
			input:   "https://coordinator.example/auth/" + testAuthID + "/extra",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAuthID(tt.input)

			if tt.wantErr {
				require.Error(t, err, "parseAuthID(%q) unexpectedly succeeded", tt.input)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestResolveAuthID pins the source contract: exactly
// one of positional argument, --url or --auth-id, each
// accepting a bare ID or a full auth URL.
func TestResolveAuthID(t *testing.T) {
	authURL := "https://coordinator.example/auth/" + testAuthID

	tests := []struct {
		name        string
		args        []string
		flags       map[string]string
		want        types.AuthID
		wantErr     bool
		errContains string
	}{
		{
			name: "positional auth URL",
			args: []string{authURL},
			want: types.AuthID(testAuthID),
		},
		{
			name: "positional bare auth ID",
			args: []string{testAuthID},
			want: types.AuthID(testAuthID),
		},
		{
			name:  "url flag",
			flags: map[string]string{"url": authURL},
			want:  types.AuthID(testAuthID),
		},
		{
			name:  "auth-id flag",
			flags: map[string]string{"auth-id": testAuthID},
			want:  types.AuthID(testAuthID),
		},
		{
			name:  "auth-id flag holding an auth URL",
			flags: map[string]string{"auth-id": authURL},
			want:  types.AuthID(testAuthID),
		},
		{
			name:        "no source",
			wantErr:     true,
			errContains: "auth ID is required",
		},
		{
			name: "positional and url flag",
			args: []string{authURL},
			flags: map[string]string{
				"url": authURL,
			},
			wantErr:     true,
			errContains: "mutually exclusive",
		},
		{
			name: "positional and auth-id flag",
			args: []string{testAuthID},
			flags: map[string]string{
				"auth-id": testAuthID,
			},
			wantErr:     true,
			errContains: "mutually exclusive",
		},
		{
			name: "url and auth-id flag",
			flags: map[string]string{
				"url":     authURL,
				"auth-id": testAuthID,
			},
			wantErr:     true,
			errContains: "mutually exclusive",
		},
		{
			name: "all three sources",
			args: []string{authURL},
			flags: map[string]string{
				"url":     authURL,
				"auth-id": testAuthID,
			},
			wantErr:     true,
			errContains: "mutually exclusive",
		},
		{
			name:        "invalid positional value",
			args:        []string{"https://coordinator.example/other/" + testAuthID},
			wantErr:     true,
			errContains: "invalid auth URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, value := range tt.flags {
				setAuthFlag(t, authApproveCmd, name, value)
			}

			got, err := resolveAuthID(authApproveCmd, tt.args)

			if tt.wantErr {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.errContains)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// runAuthCommand executes an auth subcommand by its
// root path (e.g. ["auth", "approve", ...args]).
// Execute always dispatches through the root command,
// so the full path must be set on it.
func runAuthCommand(t *testing.T, cmdPath []string) error {
	t.Helper()

	rootCmd.SetArgs(cmdPath)

	return rootCmd.Execute()
}

// TestAuthApproveCommandResolvesAuthIDFromURL pins the
// end-to-end fix: the full auth URL copied verbatim
// from the SSH session output (or passed via --url, or
// the bare --auth-id form) resolves to the same auth
// ID in the API request the coordinator receives.
func TestAuthApproveCommandResolvesAuthIDFromURL(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "positional auth URL",
			args: []string{"AUTH_URL"},
		},
		{
			name: "url flag",
			args: []string{"--url", "AUTH_URL"},
		},
		{
			name: "auth-id flag with URL",
			args: []string{"--auth-id", "AUTH_URL"},
		},
		{
			name: "bare auth-id flag",
			args: []string{"--auth-id", testAuthID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetAuthFlags(t, authApproveCmd)

			var (
				called             atomic.Bool
				gotAuthID, gotUser string
			)

			srv := authAPIFixture(t, &called, &gotAuthID, &gotUser)
			setViperCLIConfig(t, srv.URL)

			authURL := srv.URL + "/auth/" + testAuthID

			cmdArgs := make([]string, 0, len(tt.args))
			for _, arg := range tt.args {
				if arg == "AUTH_URL" {
					arg = authURL
				}

				cmdArgs = append(cmdArgs, arg)
			}

			require.NoError(t, runAuthCommand(t, append([]string{"auth", "approve"}, cmdArgs...)))

			require.True(t, called.Load(), "no API request was made")
			assert.Equal(t, testAuthID, gotAuthID)
			assert.Empty(t, gotUser)
		})
	}
}

// TestAuthRegisterCommandResolvesAuthIDFromURL pins the
// register wiring: the full registration URL resolves to
// the auth ID and the --user flag still reaches the API.
func TestAuthRegisterCommandResolvesAuthIDFromURL(t *testing.T) {
	resetAuthFlags(t, authRegisterCmd)

	var (
		called             atomic.Bool
		gotAuthID, gotUser string
	)

	srv := authAPIFixture(t, &called, &gotAuthID, &gotUser)
	setViperCLIConfig(t, srv.URL)
	setAuthFlag(t, authRegisterCmd, "user", "alice")

	require.NoError(
		t,
		runAuthCommand(t, []string{
			"auth", "register", "--user", "alice", srv.URL + "/register/" + testAuthID,
		}),
	)

	require.True(t, called.Load(), "no API request was made")
	assert.Equal(t, testAuthID, gotAuthID)
	assert.Equal(t, "alice", gotUser)
}

// TestAuthCommandsRejectInvalidInputBeforeAPI pins the
// fail-closed contract: input that is neither a bare
// auth ID nor an /auth/{id} or /register/{id} URL is a
// usage error, and no API request is made.
func TestAuthCommandsRejectInvalidInputBeforeAPI(t *testing.T) {
	tests := []struct {
		name    string
		cmdPath []string
	}{
		{
			name:    "approve with URL of unknown path kind",
			cmdPath: []string{"auth", "approve", "https://coordinator.example/other/" + testAuthID},
		},
		{
			name:    "approve with garbage",
			cmdPath: []string{"auth", "approve", "not-an-auth-id"},
		},
		{
			name:    "approve with conflicting sources",
			cmdPath: []string{"auth", "approve", "--auth-id", testAuthID, testAuthID},
		},
		{
			name:    "reject with URL of unknown path kind",
			cmdPath: []string{"auth", "reject", "https://coordinator.example/other/" + testAuthID},
		},
		{
			name:    "register with garbage",
			cmdPath: []string{"auth", "register", "--user", "alice", "not-an-auth-id"},
		},
		{
			name:    "approve with no source",
			cmdPath: []string{"auth", "approve"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetAuthFlags(t, authApproveCmd)
			resetAuthFlags(t, authRejectCmd)
			resetAuthFlags(t, authRegisterCmd)

			var (
				called             atomic.Bool
				gotAuthID, gotUser string
			)

			srv := authAPIFixture(t, &called, &gotAuthID, &gotUser)
			setViperCLIConfig(t, srv.URL)

			err := runAuthCommand(t, tt.cmdPath)

			require.Error(t, err)
			require.False(t, called.Load(), "API was called despite invalid input")
			assert.Empty(t, gotAuthID)
		})
	}
}
