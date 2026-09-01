package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tomiwebpro/stealthscale/hscontrol/util"
)

func init() {
	rootCmd.AddCommand(dumpConfigCmd)
}

var dumpConfigCmd = &cobra.Command{
	Use:    "dumpConfig",
	Short:  "dump current config to config.dump.yaml, integration test only",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dumpPath := "/etc/stealthscale/config.dump.yaml"
		if runtime.GOOS == "windows" {
			if pd := os.Getenv("ProgramData"); pd != "" {
				dumpPath = filepath.Join(pd, "stealthscale", "config.dump.yaml")
			} else {
				dumpPath = `C:\ProgramData\stealthscale\config.dump.yaml`
			}
		}
		if err := util.EnsureDir(filepath.Dir(dumpPath)); err != nil {
			return fmt.Errorf("ensuring dump dir: %w", err)
		}
		err := viper.WriteConfigAs(dumpPath)
		if err != nil {
			return fmt.Errorf("dumping config: %w", err)
		}

		return nil
	},
}
