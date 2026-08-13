package cli

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/tailscale/squibble"
)

func init() {
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Launches the stealthscale server",
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := newStealthScaleServerWithConfig()
		if err != nil {
			if squibbleErr, ok := errors.AsType[squibble.ValidationError](err); ok {
				fmt.Printf("SQLite schema failed to validate:\n")
				fmt.Println(squibbleErr.Diff)
			}

			return fmt.Errorf("initializing: %w", err)
		}

		err = app.Serve()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("stealthscale ran into an error and had to shut down: %w", err)
		}

		return nil
	},
}
