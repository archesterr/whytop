package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/archesterr/whytop/internal/tui"
)

var version = "dev"

func main() {
	interval := flag.Duration("i", 2*time.Second, "sampling interval (min 500ms)")
	port := flag.Int("port", 0, "open the process listening on this port")
	pid := flag.Int("pid", 0, "open this PID")
	host := flag.String("host", "", "open a remote host over SSH, as [user@]host[:port] (default: this machine)")
	sshConfig := flag.String("ssh-config", "", "ssh config to read hosts from (default ~/.ssh/config)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("whytop", version)
		return
	}
	// The local collector is Linux-only, but a remote host is read over SSH
	// and its /proc is Linux whatever this machine is — so -host works from
	// a Mac even though watching the Mac itself does not.
	if runtime.GOOS != "linux" && *host == "" {
		fmt.Fprintln(os.Stderr, "whytop watches Linux; use -host to read a Linux box over SSH")
		os.Exit(1)
	}
	if *interval < 500*time.Millisecond {
		*interval = 500 * time.Millisecond
	}
	if os.Geteuid() != 0 && *host == "" {
		fmt.Fprintln(os.Stderr, "note: not running as root; other users' sockets, disk I/O and open files are hidden")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := tui.Run(ctx, tui.Options{Interval: *interval, Version: version, PID: *pid, Port: *port,
		Host: *host, SSHConfig: *sshConfig}); err != nil {
		fmt.Fprintln(os.Stderr, "whytop:", err)
		os.Exit(1)
	}
}
