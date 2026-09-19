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
	"github.com/archesterr/whytop/internal/web"
)

var version = "dev"

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "address to serve on; port 0 picks a random free port")
	interval := flag.Duration("i", 2*time.Second, "sampling interval (min 500ms)")
	token := flag.String("token", "", "access token (random when empty)")
	port := flag.Int("port", 0, "open the process listening on this port")
	pid := flag.Int("pid", 0, "open this PID")
	useTUI := flag.Bool("tui", false, "run in the terminal instead of starting a web server (no browser, no tunnel — just SSH in)")
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

	var err error
	if *useTUI {
		err = tui.Run(ctx, tui.Options{Interval: *interval, Version: version, PID: *pid, Port: *port})
	} else {
		err = web.Run(ctx, web.Options{
			Listen:   *listen,
			Interval: *interval,
			Token:    *token,
			Version:  version,
			PID:      *pid,
			Port:     *port,
		})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "whytop:", err)
		os.Exit(1)
	}
}
