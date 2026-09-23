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
	showVersion := flag.Bool("version", false, "print version and exit")
	noUpdate := flag.Bool("no-update-check", false, "never check whether a newer release exists (same as WHYTOP_NO_UPDATE_CHECK=1)")
	flag.Parse()

	// Set rather than passed through Options: the check runs inside a
	// bubbletea command with no access to the flags, and an environment
	// variable is what the collector already reads for this kind of switch.
	// It also means the flag and the variable cannot disagree.
	if *noUpdate {
		os.Setenv("WHYTOP_NO_UPDATE_CHECK", "1")
	}

	if *showVersion {
		fmt.Println("whytop", version)
		return
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "whytop watches the Linux machine it runs on")
		os.Exit(1)
	}
	if *interval < 500*time.Millisecond {
		*interval = 500 * time.Millisecond
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "note: not running as root; other users' sockets, disk I/O and open files are hidden")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := tui.Run(ctx, tui.Options{Interval: *interval, Version: version, PID: *pid, Port: *port}); err != nil {
		fmt.Fprintln(os.Stderr, "whytop:", err)
		os.Exit(1)
	}
}
