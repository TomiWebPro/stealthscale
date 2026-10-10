package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	clientv1 "github.com/tomiwebpro/stealthscale/gen/client/v1"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

func init() {
	rootCmd.AddCommand(authCmd)

	authRegisterCmd.Flags().StringP("user", "u", "", "User")
	authRegisterCmd.Flags().String("auth-id", "", "Auth ID (bare ID or full auth URL)")
	authRegisterCmd.Flags().String(
		"url", "",
		"Auth URL (<coordinator>/auth/<AUTH_ID> or <coordinator>/register/<AUTH_ID>)",
	)
	mustMarkRequired(authRegisterCmd, "user")
	authCmd.AddCommand(authRegisterCmd)

	authApproveCmd.Flags().String("auth-id", "", "Auth ID (bare ID or full auth URL)")
	authApproveCmd.Flags().String(
		"url", "",
		"Auth URL (<coordinator>/auth/<AUTH_ID> or <coordinator>/register/<AUTH_ID>)",
	)
	authCmd.AddCommand(authApproveCmd)

	authRejectCmd.Flags().String("auth-id", "", "Auth ID (bare ID or full auth URL)")
	authRejectCmd.Flags().String(
		"url", "",
		"Auth URL (<coordinator>/auth/<AUTH_ID> or <coordinator>/register/<AUTH_ID>)",
	)
	authCmd.AddCommand(authRejectCmd)
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage node authentication and approval",
}

var authRegisterCmd = &cobra.Command{
	Use:   "register [AUTH_ID_OR_URL]",
	Short: "Register a node to your network",
	Args:  cobra.MaximumNArgs(1),
	RunE: clientRunE(func(ctx context.Context, client *clientv1.ClientWithResponses, cmd *cobra.Command, args []string) error {
		user, _ := cmd.Flags().GetString("user")

		authID, err := resolveAuthID(cmd, args)
		if err != nil {
			return err
		}

		authIDStr := authID.String()

		resp, err := client.AuthRegisterWithResponse(ctx, clientv1.AuthRegisterJSONRequestBody{
			AuthId: &authIDStr,
			User:   &user,
		})
		if err != nil {
			return fmt.Errorf("registering node: %w", err)
		}

		if resp.StatusCode() != http.StatusOK {
			return apiError(resp.StatusCode(), resp.ApplicationproblemJSONDefault)
		}

		node := resp.JSON200.Node

		return printOutput(
			cmd,
			node,
			fmt.Sprintf("Node %s registered", node.GivenName),
		)
	}),
}

var authApproveCmd = &cobra.Command{
	Use:   "approve [AUTH_ID_OR_URL]",
	Short: "Approve a pending authentication request",
	Args:  cobra.MaximumNArgs(1),
	RunE: clientRunE(func(ctx context.Context, client *clientv1.ClientWithResponses, cmd *cobra.Command, args []string) error {
		authID, err := resolveAuthID(cmd, args)
		if err != nil {
			return err
		}

		authIDStr := authID.String()

		resp, err := client.AuthApproveWithResponse(ctx, clientv1.AuthApproveJSONRequestBody{AuthId: &authIDStr})
		if err != nil {
			return fmt.Errorf("approving auth request: %w", err)
		}

		if resp.StatusCode() != http.StatusOK {
			return apiError(resp.StatusCode(), resp.ApplicationproblemJSONDefault)
		}

		return printOutput(cmd, resp.JSON200, "Auth request approved")
	}),
}

var authRejectCmd = &cobra.Command{
	Use:   "reject [AUTH_ID_OR_URL]",
	Short: "Reject a pending authentication request",
	Args:  cobra.MaximumNArgs(1),
	RunE: clientRunE(func(ctx context.Context, client *clientv1.ClientWithResponses, cmd *cobra.Command, args []string) error {
		authID, err := resolveAuthID(cmd, args)
		if err != nil {
			return err
		}

		authIDStr := authID.String()

		resp, err := client.AuthRejectWithResponse(ctx, clientv1.AuthRejectJSONRequestBody{AuthId: &authIDStr})
		if err != nil {
			return fmt.Errorf("rejecting auth request: %w", err)
		}

		if resp.StatusCode() != http.StatusOK {
			return apiError(resp.StatusCode(), resp.ApplicationproblemJSONDefault)
		}

		return printOutput(cmd, resp.JSON200, "Auth request rejected")
	}),
}

// resolveAuthID resolves the auth ID an auth command operates on
// from exactly one of: a positional argument, --url, or --auth-id.
// The positional argument and --url accept a full auth URL of the
// form <coordinator>/auth/<id> or <coordinator>/register/<id> —
// the only handle on a pending session, printed by `tailscale ssh`
// in check mode and rendered by the web auth provider — so the
// operator can copy it verbatim. --auth-id keeps accepting a bare
// auth ID (a full auth URL works there too). Passing none, or
// more than one, is a usage error: the command must never silently
// target a different session than the one the operator copied.
func resolveAuthID(cmd *cobra.Command, args []string) (types.AuthID, error) {
	urlFlag, _ := cmd.Flags().GetString("url")
	authIDFlag, _ := cmd.Flags().GetString("auth-id")

	sources := 0
	if len(args) > 0 {
		sources++
	}

	if urlFlag != "" {
		sources++
	}

	if authIDFlag != "" {
		sources++
	}

	if sources == 0 {
		return "", errors.New("auth ID is required: pass --auth-id <AUTH_ID> or an auth URL like <coordinator>/auth/<AUTH_ID>")
	}

	if sources > 1 {
		return "", errors.New("specify the auth ID only once: --auth-id, --url and the positional argument are mutually exclusive")
	}

	input := authIDFlag
	if input == "" {
		input = urlFlag
	}

	if input == "" {
		input = args[0]
	}

	return parseAuthID(input)
}

// parseAuthID accepts a bare auth ID or a full auth-flow URL of
// the form <coordinator>/auth/<id> or <coordinator>/register/<id>
// (the URL shapes printed by `tailscale ssh` in check mode and
// rendered by the web auth provider) and returns the auth ID it
// refers to. Anything else is an error, so the command fails
// before any API call instead of silently targeting a different
// session.
func parseAuthID(input string) (types.AuthID, error) {
	u, err := url.Parse(input)
	if err == nil && (u.Scheme != "" || strings.HasPrefix(input, "/")) {
		id, ok := authIDFromURLPath(u.Path)
		if !ok {
			return "", fmt.Errorf(
				"invalid auth URL %q: expected path /auth/<AUTH_ID> or /register/<AUTH_ID>",
				input,
			)
		}

		authID, err := types.AuthIDFromString(id)
		if err != nil {
			return "", fmt.Errorf("invalid auth ID in URL %q: %w", input, err)
		}

		return authID, nil
	}

	authID, err := types.AuthIDFromString(input)
	if err != nil {
		return "", fmt.Errorf("invalid auth ID %q: %w", input, err)
	}

	return authID, nil
}

// authIDFromURLPath extracts the auth ID from a /auth/<id> or
// /register/<id> path; ok is false for any other path shape.
func authIDFromURLPath(path string) (string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 2 || (segments[0] != "auth" && segments[0] != "register") {
		return "", false
	}

	return segments[1], true
}
