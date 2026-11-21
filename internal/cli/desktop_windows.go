//go:build windows

package cli

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// RunDesktop is the double-click entry point on Windows. It starts a local
// signaling server and a node with a userspace TUN, opens the control panel in
// the default browser, and stays alive so the daemons keep running.
func RunDesktop(self string) {
	hideConsole()

	server := exec.Command(self, "server",
		"--listen", "127.0.0.1:19000",
		"--admin", "127.0.0.1:19001",
		"--subnet", "10.66.0.0/24")
	if err := server.Start(); err != nil {
		writeDesktopError(err)
		return
	}

	time.Sleep(time.Second)

	deviceName := "Desktop"
	if h, err := os.Hostname(); err == nil && h != "" {
		deviceName = "Desktop " + h
	}
	node := exec.Command(self, "node",
		"--server", "127.0.0.1:19000",
		"--name", deviceName,
		"--tun", "userspace",
		"--gui", "127.0.0.1:9050")
	if err := node.Start(); err != nil {
		writeDesktopError(err)
		_ = server.Process.Kill()
		return
	}

	for i := 0; i < 80; i++ {
		c, err := net.DialTimeout("tcp", "127.0.0.1:9050", 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler",
				"http://127.0.0.1:9050/").Start()
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Keep the process alive while the local network is in use.
	_ = node.Wait()
	_ = server.Process.Kill()
}

func writeDesktopError(err error) {
	dir, e := os.UserConfigDir()
	if e != nil {
		return
	}
	p := filepath.Join(dir, "DevicePort", "desktop-error.txt")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(err.Error()+"\n"), 0o644)
}

// hideConsole detaches the (otherwise flashing) console window of the
// double-clicked executable. CLI invocations with arguments keep their console.
func hideConsole() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	u32 := syscall.NewLazyDLL("user32.dll")
	get := k32.NewProc("GetConsoleWindow")
	show := u32.NewProc("ShowWindow")
	h, _, _ := get.Call()
	if h != 0 {
		show.Call(h, 0) // SW_HIDE
	}
}
