package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func init() {
	uninstallCmd.Flags().Bool("purge", false, "remove config, database and state (otherwise keeps %ProgramData%/stealthscale, /var/lib/stealthscale, /Library/Application Support/stealthscale)")
	uninstallCmd.Flags().Bool("yes", false, "skip confirmation prompt")
	uninstallCmd.Flags().BoolP("force", "f", false, "skip confirmation prompt (alias for --yes)")
	_ = uninstallCmd.Flags().MarkHidden("force")
	rootCmd.AddCommand(uninstallCmd)
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Clean uninstall of StealthScale (service, binary, config, state)",
	Long: `Clean uninstall for all distributions.

Linux (systemd/deb):
  - stops & disables stealthscale.service
  - removes binary (/usr/bin/stscale, /usr/local/bin/stscale)
  - on --purge also removes /etc/stealthscale, /var/lib/stealthscale, stealthscale user and runtime dir
  - for deb installs, 'sudo apt purge stealthscale' also works (calls deb postrm/purge)

Windows (service + named pipe):
  - stops & deletes StealthScale service (sc.exe)
  - removes %ProgramFiles%\stealthscale\stscale.exe
  - removes HKCU\Software\Microsoft\Windows\CurrentVersion\Run StealthScale
  - on --purge also removes %ProgramData%\stealthscale (config, db, .xray_secret)

macOS (launchd):
  - unloads /Library/LaunchDaemons/com.stealthscale.plist
  - removes /usr/local/bin/stscale, /usr/local/etc/stealthscale, /Library/Application Support/stealthscale, /Library/Logs/stealthscale, /var/run/stealthscale
  - on --purge also removes config/state (same as above; without --purge logs are kept)

Requires elevation (sudo / Admin PowerShell) for service/binary removal.
Without elevation it still cleans user state (--purge) and prints manual steps.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		purge, _ := cmd.Flags().GetBool("purge")
		yes, _ := cmd.Flags().GetBool("yes")
		force, _ := cmd.Flags().GetBool("force")
		if force {
			yes = true
		}
		if !yes && !confirmAction(cmd, fmt.Sprintf("Uninstall StealthScale (%s, purge=%v)?", runtime.GOOS, purge)) {
			fmt.Println("aborted")
			return nil
		}
		switch runtime.GOOS {
		case "linux":
			return uninstallLinux(purge)
		case "windows":
			return uninstallWindows(purge)
		case "darwin":
			return uninstallDarwin(purge)
		default:
			return fmt.Errorf("uninstall not supported on %s; remove binary and config manually", runtime.GOOS)
		}
	},
}

func uninstallLinux(purge bool) error {
	fmt.Println("[*] Uninstalling StealthScale (Linux)")
	// Try systemd if present
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		_ = runCmd("systemctl", "stop", "stealthscale.service")
		_ = runCmd("systemctl", "disable", "stealthscale.service")
		_ = runCmd("systemctl", "daemon-reload")
	} else {
		fmt.Println("[*] systemd not detected, skipping service stop")
	}
	// Binaries
	for _, p := range []string{"/usr/bin/stscale", "/usr/local/bin/stscale"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			fmt.Printf("[!] remove %s: %v (try sudo)\n", p, err)
		} else if err == nil {
			fmt.Printf("[*] removed %s\n", p)
		}
	}
	if purge {
		for _, p := range []string{"/etc/stealthscale", "/var/lib/stealthscale", "/var/lib/coordination", "/var/run/stealthscale", "/run/stealthscale"} {
			if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
				fmt.Printf("[!] remove %s: %v\n", p, err)
			} else {
				fmt.Printf("[*] removed %s\n", p)
			}
		}
		// userdel (best effort)
		_ = runCmd("userdel", "stealthscale")
		_ = runCmd("userdel", "coordination")
		if _, err := exec.LookPath("deb-systemd-helper"); err == nil {
			_ = runCmd("deb-systemd-helper", "purge", "stealthscale.service")
		}
		fmt.Println("[*] purge complete (config, state and user removed)")
	} else {
		fmt.Println("[*] kept /etc/stealthscale and /var/lib/stealthscale (use --purge to delete)")
		fmt.Println("[*] For deb installs: sudo apt remove stealthscale  (keep data)  |  sudo apt purge stealthscale  (delete data)")
	}
	fmt.Println("[*] Linux uninstall done. If installed via deb, also run: sudo apt autoremove")
	return nil
}

func uninstallWindows(purge bool) error {
	fmt.Println("[*] Uninstalling StealthScale (Windows)")
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	installDir := filepath.Join(programFiles, "stealthscale")
	configDir := filepath.Join(programData, "stealthscale")
	serviceName := "StealthScale"

	// Stop & delete service (try PowerShell Stop-Service then sc.exe)
	_ = runCmd("powershell", "-Command", fmt.Sprintf("Stop-Service -Name %s -Force -ErrorAction SilentlyContinue", serviceName))
	_ = runCmd("sc.exe", "stop", serviceName)
	// Give it a moment
	_ = runCmd("sc.exe", "delete", serviceName)
	fmt.Printf("[*] service %s removed (if it existed)\n", serviceName)

	// Remove Run key (tray autostart)
	_ = runCmd("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "StealthScale", "/f")
	// Also try PowerShell for HKCU
	_ = runCmd("powershell", "-Command", `Remove-ItemProperty -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run" -Name "StealthScale" -ErrorAction SilentlyContinue`)
	fmt.Println("[*] removed autostart registry key StealthScale (if present)")

	// Remove binary dir
	if err := os.RemoveAll(installDir); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[!] remove %s: %v (run as Admin)\n", installDir, err)
	} else {
		fmt.Printf("[*] removed %s\n", installDir)
	}
	if purge {
		if err := os.RemoveAll(configDir); err != nil && !os.IsNotExist(err) {
			fmt.Printf("[!] remove %s: %v\n", configDir, err)
		} else {
			fmt.Printf("[*] removed %s\n", configDir)
		}
		fmt.Println("[*] purge complete (ProgramData config, db, .xray_secret removed)")
	} else {
		fmt.Printf("[*] kept %s (use --purge to delete config & db)\n", configDir)
	}
	// Also remove named pipe is ephemeral (no file)

	fmt.Println("[*] Windows uninstall done. Manual fallback if needed:")
	fmt.Printf("  sc.exe stop %s & sc.exe delete %s\n", serviceName, serviceName)
	fmt.Printf("  Remove-Item -Recurse \"%s\",\"%s\"\n", installDir, configDir)
	fmt.Printf("  reg delete HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run /v StealthScale /f\n")
	return nil
}

func uninstallDarwin(purge bool) error {
	fmt.Println("[*] Uninstalling StealthScale (macOS)")
	plistDst := "/Library/LaunchDaemons/com.stealthscale.plist"
	// unload
	_ = runCmd("launchctl", "unload", plistDst)
	_ = runCmd("launchctl", "bootout", "system/"+plistDst)
	_ = os.Remove(plistDst)
	fmt.Printf("[*] removed %s (if existed)\n", plistDst)

	for _, p := range []string{"/usr/local/bin/stscale", "/opt/homebrew/bin/stscale"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			fmt.Printf("[!] remove %s: %v (try sudo)\n", p, err)
		} else if err == nil {
			fmt.Printf("[*] removed %s\n", p)
		}
	}
	pathsPurge := []string{"/usr/local/etc/stealthscale", "/Library/Application Support/stealthscale", "/Library/Logs/stealthscale", "/var/run/stealthscale"}
	pathsKeep := []string{"/usr/local/etc/stealthscale", "/Library/Application Support/stealthscale"}
	if purge {
		for _, p := range pathsPurge {
			if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
				fmt.Printf("[!] remove %s: %v\n", p, err)
			} else {
				fmt.Printf("[*] removed %s\n", p)
			}
		}
		fmt.Println("[*] purge complete (config, state, logs removed)")
	} else {
		for _, p := range pathsKeep {
			fmt.Printf("[*] kept %s (use --purge to delete)\n", p)
		}
		// still remove logs? keep for debug
		fmt.Println("[*] kept /Library/Logs/stealthscale (use --purge to delete)")
	}
	fmt.Println("[*] macOS uninstall done. If installed via brew: brew uninstall stealthscale")
	return nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Debug().Err(err).Str("cmd", name).Msg("uninstall step failed (continuing)")
	}
	return err
}
