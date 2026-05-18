package web

import (
	"os/exec"
	"runtime"
)

// OpenBrowser tries to open url with the OS default browser. Best-effort: any
// error is returned to the caller, which logs it but keeps the server running.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
