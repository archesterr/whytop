package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/archesterr/whytop/internal/actions"
	"github.com/archesterr/whytop/internal/collect"
)

//go:embed static/index.html
var indexHTML []byte

type Options struct {
	Listen   string
	Interval time.Duration
	Token    string
	Version  string
	PID      int
	Port     int
}

type Server struct {
	opt  Options
	col  *collect.Collector
	kick chan struct{}

	connsAt atomic.Int64 // last time a client asked for sockets (unix nano)

	mu   sync.RWMutex
	snap *collect.Snapshot
	raw  []byte
	gz   []byte
}

type payload struct {
	*collect.Snapshot
	Version  string
	Interval int64
}

func Run(ctx context.Context, opt Options) error {
	if opt.Token == "" {
		opt.Token = newToken()
	}
	ln, err := net.Listen("tcp", opt.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", opt.Listen, err)
	}
	s := &Server{opt: opt, col: collect.New(), kick: make(chan struct{}, 1)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/snapshot", s.auth(s.handleSnapshot))
	mux.HandleFunc("GET /api/proc/{pid}", s.auth(s.handleProc))
	mux.HandleFunc("GET /api/journal/{pid}", s.auth(s.handleJournal))
	mux.HandleFunc("POST /api/signal", s.auth(s.handleSignal))
	mux.HandleFunc("POST /api/restart", s.auth(s.handleRestart))

	srv := &http.Server{Handler: withHeaders(mux), ReadHeaderTimeout: 5 * time.Second}
	go s.loop(ctx)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	s.announce(ln.Addr().(*net.TCPAddr))
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) announce(addr *net.TCPAddr) {
	frag := ""
	switch {
	case s.opt.PID > 0:
		frag = fmt.Sprintf("#pid=%d", s.opt.PID)
	case s.opt.Port > 0:
		frag = fmt.Sprintf("#port=%d", s.opt.Port)
	}
	port := strconv.Itoa(addr.Port)
	hosts := []string{addr.IP.String()}
	if addr.IP.IsUnspecified() {
		hosts = localIPs()
	}

	fmt.Printf("\n  whytop %s\n\n", s.opt.Version)
	for _, h := range hosts {
		fmt.Printf("  http://%s/?t=%s%s\n", net.JoinHostPort(h, port), s.opt.Token, frag)
	}
	if addr.IP.IsLoopback() {
		fmt.Printf("\n  On a remote server: ssh -L %s:127.0.0.1:%s <server>, then open the URL above.\n", port, port)
	} else {
		fmt.Println("\n  Warning: reachable from the network. Anyone with this URL can stop processes.")
	}
	fmt.Print("  Press Ctrl+C to stop.\n\n")
}

func localIPs() []string {
	ips := []string{"127.0.0.1"}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			ips = append(ips, n.IP.String())
		}
	}
	return ips
}

func (s *Server) loop(ctx context.Context) {
	s.col.Collect() // prime counters so the first sample has real rates
	select {
	case <-ctx.Done():
		return
	case <-time.After(500 * time.Millisecond):
	}
	for {
		s.col.WantConns.Store(time.Since(time.Unix(0, s.connsAt.Load())) < 15*time.Second)
		snap := s.col.Collect()
		raw, err := json.Marshal(payload{Snapshot: snap, Version: s.opt.Version, Interval: s.opt.Interval.Milliseconds()})
		if err != nil {
			log.Printf("encode snapshot: %v", err)
		} else {
			gz := gzipBytes(raw)
			s.mu.Lock()
			s.snap, s.raw, s.gz = snap, raw, gz
			s.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.opt.Interval):
		case <-s.kick:
		}
	}
}

// poke requests an immediate sample.
func (s *Server) poke() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Server) proc(pid int32) (collect.Proc, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snap == nil {
		return collect.Proc{}, false
	}
	i, ok := s.snap.ByPID[pid]
	if !ok {
		return collect.Proc{}, false
	}
	return s.snap.Procs[i], true
}

// ------------------------------------------------------------------ handlers

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !s.validToken(r.URL.Query().Get("t")) {
		http.Error(w, "whytop: missing or wrong access token. Open the exact URL printed in the terminal.", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("conns") == "1" {
		prev := s.connsAt.Swap(time.Now().UnixNano())
		if time.Since(time.Unix(0, prev)) > 15*time.Second {
			s.poke() // sockets were off: sample now instead of waiting a full interval
		}
	}
	s.mu.RLock()
	raw, gz := s.raw, s.gz
	s.mu.RUnlock()
	if raw == nil {
		writeErr(w, http.StatusServiceUnavailable, "collecting the first sample")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		_, _ = w.Write(gz)
		return
	}
	_, _ = w.Write(raw)
}

type procResponse struct {
	collect.Extra
	UnitStatus     map[string]string
	RestartBlocked string
}

func (s *Server) handleProc(w http.ResponseWriter, r *http.Request) {
	pid, ok := pidParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid PID")
		return
	}
	p, ok := s.proc(pid)
	if !ok {
		writeErr(w, http.StatusNotFound, "process not found")
		return
	}
	resp := procResponse{Extra: collect.ProcExtra(pid)}
	if strings.HasSuffix(p.Unit, ".service") && !p.UnitUser {
		resp.UnitStatus = actions.UnitStatus(p.Unit)
	}
	if err := actions.CanRestart(p.Unit, p.UnitUser); err != nil {
		resp.RestartBlocked = err.Error()
	}
	writeJSON(w, resp)
}

func (s *Server) handleJournal(w http.ResponseWriter, r *http.Request) {
	pid, ok := pidParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid PID")
		return
	}
	p, _ := s.proc(pid)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, actions.Journal(p.Unit, p.UnitUser, pid, 100))
}

func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PID    int32  `json:"pid"`
		Signal string `json:"signal"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sig, ok := map[string]syscall.Signal{"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL}[req.Signal]
	if !ok {
		writeErr(w, http.StatusBadRequest, "signal must be TERM or KILL")
		return
	}
	if _, ok := s.proc(req.PID); !ok {
		writeErr(w, http.StatusNotFound, "process not found")
		return
	}
	if err := actions.Signal(req.PID, sig); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.poke()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PID int32 `json:"pid"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, ok := s.proc(req.PID)
	if !ok {
		writeErr(w, http.StatusNotFound, "process not found")
		return
	}
	if err := actions.CanRestart(p.Unit, p.UnitUser); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := actions.RestartUnit(p.Unit); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st := actions.UnitStatus(p.Unit)
	mainPID, _ := strconv.Atoi(st["MainPID"])
	s.poke()
	writeJSON(w, map[string]any{"unit": p.Unit, "state": st["ActiveState"], "mainPID": mainPID})
}

// ------------------------------------------------------------------- helpers

func (s *Server) validToken(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.opt.Token)) == 1
}

// auth requires the token in a custom header, which also blocks cross-site
// requests (browsers cannot add custom headers without a CORS preflight).
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validToken(r.Header.Get("X-Whytop-Token")) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	}
}

func withHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func pidParam(r *http.Request) (int32, bool) {
	n, err := strconv.ParseInt(r.PathValue("pid"), 10, 32)
	return int32(n), err == nil && n > 0
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

func newToken() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
