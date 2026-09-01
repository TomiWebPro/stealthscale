package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/tailscale/squibble"
	"github.com/tomiwebpro/stealthscale/hscontrol/tray"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

var serveTray bool

func init() {
	serveCmd.Flags().BoolVar(&serveTray, "tray", false, "run with Windows system tray (hide-in-tray, Windows only; no effect on Linux/macOS)")
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Launches the stealthscale server",
	RunE: func(cmd *cobra.Command, args []string) error {
		if serveTray && runtime.GOOS != "windows" {
			log.Warn().Msg("--tray is Windows only, ignoring")
			serveTray = false
		}
		app, err := newStealthScaleServerWithConfig()
		if err != nil {
			if squibbleErr, ok := errors.AsType[squibble.ValidationError](err); ok {
				fmt.Printf("SQLite schema failed to validate:\n")
				fmt.Println(squibbleErr.Diff)
			}

			return fmt.Errorf("initializing: %w", err)
		}

		if serveTray {
			return runServeWithTray(app)
		}

		err = app.Serve()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("stealthscale ran into an error and had to shut down: %w", err)
		}

		return nil
	},
}

func runServeWithTray(app interface {
	Serve() error
	GetConfig() *types.Config
}) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		if err := app.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		} else {
			close(errCh)
		}
	}()

	// Give the server a moment to fail fast (e.g. bad config, port in use)
	select {
	case err, ok := <-errCh:
		if ok && err != nil {
			return err
		}
		if !ok {
			return nil
		}
	case <-time.After(400 * time.Millisecond):
	}

	version := types.GetVersionInfo().Version
	cfg := app.GetConfig()

	done := make(chan struct{})
	go func() {
		select {
		case err, ok := <-errCh:
			if ok && err != nil {
				log.Error().Err(err).Msg("server exited unexpectedly")
			}
			cancel()
		case <-ctx.Done():
		}
		close(done)
	}()

	tray.Run(ctx, cfg, version, func() {
		log.Info().Msg("tray quit, shutting down server")
		// Trigger the signal handler inside Serve via OS signal
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(os.Interrupt)
			_ = p.Signal(syscall.SIGTERM)
		}
		// Ensure we exit even if signal not caught (Windows service edge)
		go func() {
			time.Sleep(5 * time.Second)
			log.Warn().Msg("graceful shutdown timed out, exiting")
			os.Exit(0)
		}()
		cancel()
	})

	cancel()
	<-done
	select {
	case err, ok := <-errCh:
		if ok && err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-time.After(5 * time.Second):
	}
	return nil
}
