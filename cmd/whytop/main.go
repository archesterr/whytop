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
	flag.Parse()

	if *showVersion {
		fmt.Println("whytop", version)
		return
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "whytop supports Linux only")
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
