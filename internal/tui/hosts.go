package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/remote"
)

// whytop opens on localhost and stays there until you say otherwise. A
// monitoring tool that asks which machine you meant before showing you
// anything is a tool you close again — the machine you are sitting on is
// the overwhelmingly common answer, so it is the default rather than a
// choice.
type hostPanel struct {
	hosts  []remote.Host
	sel    int
	adding bool
	input  string
	// configPath is the ssh_config being read. It starts at ~/.ssh/config
	// and can be pointed elsewhere, for people who keep a separate file for
	// production.
	configPath string
	configErr  string
}

// connectedMsg carries the outcome of a connection attempt. Dialling blocks
// on the network, so it happens in a tea.Cmd and comes back as a message
// rather than freezing the UI mid-keystroke.
type connectedMsg struct {
	client *remote.Client
	host   remote.Host
	err    error
}

func (m *model) openHosts() (tea.Model, tea.Cmd) {
	path := m.opt.SSHConfig
	if path == "" {
		path = remote.DefaultConfigPath()
	}
	p := &hostPanel{configPath: path}
	p.reload()
	// Start the cursor on the host currently being viewed, so the panel
	// opens showing where you are rather than where you were last.
	for i, h := range p.hosts {
		if h.Label() == m.hostLabel() {
			p.sel = i
		}
	}
	m.hosts = p
	return *m, nil
}

// reload re-reads the ssh_config. localhost is always first and is not a
// configured host: it needs no SSH at all.
func (p *hostPanel) reload() {
	p.hosts = []remote.Host{{Name: "localhost"}}
	p.configErr = ""
	if p.configPath == "" {
		return
	}
	hosts, err := remote.LoadConfig(p.configPath)
	if err != nil {
		// Not having an ssh_config is normal, not an error worth shouting
		// about — you can still type a host in.
		p.configErr = fmt.Sprintf("no hosts read from %s (%v)", p.configPath, err)
		return
	}
	p.hosts = append(p.hosts, hosts...)
}

func (m model) hostLabel() string {
	if m.remote == nil {
		return "localhost"
	}
	return m.remote.Host().Label()
}

// parseTarget reads the "user@host:port" people already type everywhere
// else. Anything it can't make sense of is reported rather than guessed at.
func parseTarget(s string) (remote.Host, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return remote.Host{}, fmt.Errorf("type a host, as host, user@host, or user@host:port")
	}
	var h remote.Host
	if user, rest, ok := strings.Cut(s, "@"); ok {
		h.User, s = user, rest
	}
	if host, port, ok := strings.Cut(s, ":"); ok {
		n, err := strconv.Atoi(port)
		if err != nil || n <= 0 || n > 65535 {
			return remote.Host{}, fmt.Errorf("%q is not a port number", port)
		}
		h.Addr, h.Port = host, n
	} else {
		h.Addr = s
	}
	if h.Addr == "" {
		return remote.Host{}, fmt.Errorf("no hostname in %q", s)
	}
	h.Name = h.Addr
	return h, nil
}

const dialTimeout = 12 * time.Second

func connectCmd(h remote.Host) tea.Cmd {
	return func() tea.Msg {
		c, err := remote.Dial(h, dialTimeout)
		return connectedMsg{client: c, host: h, err: err}
	}
}

func (m model) handleHostKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.hosts
	if p.adding {
		switch msg.Type {
		case tea.KeyEsc:
			p.adding, p.input = false, ""
		case tea.KeyEnter:
			if path, ok := strings.CutPrefix(p.input, "@"); ok {
				p.configPath = strings.TrimSpace(path)
				p.adding, p.input = false, ""
				p.reload()
				p.sel = 0
				return m.showToast("Reading hosts from "+p.configPath, true)
			}
			h, err := parseTarget(p.input)
			if err != nil {
				return m.showToast(err.Error(), false)
			}
			p.adding, p.input = false, ""
			m.connecting = h.Label()
			return m, connectCmd(h)
		case tea.KeyBackspace:
			if r := []rune(p.input); len(r) > 0 {
				p.input = string(r[:len(r)-1])
			}
		case tea.KeyRunes:
			p.input += string(msg.Runes)
		case tea.KeySpace:
			p.input += " "
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "h", "q":
		m.hosts = nil
	case "up", "k":
		if p.sel > 0 {
			p.sel--
		}
	case "down", "j":
		if p.sel < len(p.hosts)-1 {
			p.sel++
		}
	case "a":
		p.adding = true
	case "r":
		p.reload()
		return m.showToast("Re-read "+p.configPath, true)
	case "c":
		p.adding = true
		p.input = p.configPath
		// Reusing the one input line for two jobs would be confusing, so
		// the config path is changed by typing it with a leading @.
		p.input = "@" + p.configPath
	case "enter":
		if p.sel >= len(p.hosts) {
			return m, nil
		}
		h := p.hosts[p.sel]
		if h.Name == "localhost" {
			return m.useLocal()
		}
		m.connecting = h.Label()
		return m, connectCmd(h)
	}
	return m, nil
}

// useLocal goes back to the machine whytop is running on, closing the SSH
// connection rather than leaving it open in the background: an idle session
// to a production box is a thing someone has to explain later.
func (m *model) useLocal() (tea.Model, tea.Cmd) {
	if m.remote != nil {
		m.remote.Close()
		m.remote = nil
	}
	m.hosts = nil
	m.snap = nil
	m.resetForHost()
	return *m, tea.Batch(m.collectCmd(0), func() tea.Msg { return clearToastMsg{gen: -1} })
}

// resetForHost drops everything that was about the previous machine. A
// selection or a locked row order carried across hosts would point at a PID
// on a different box, which is the kind of mistake that ends with the wrong
// process being killed.
func (m *model) resetForHost() {
	m.sel = ""
	m.detail = nil
	m.confirm = nil
	m.lockRank = nil
	m.findingSel = 0
}

func (m model) renderHosts(w, h int) string {
	p := m.hosts
	var b strings.Builder
	for i, host := range p.hosts {
		sel := i == p.sel
		name := host.Label()
		detail := ""
		switch {
		case host.Name == "localhost":
			detail = "this machine — no SSH"
		default:
			target := host.Addr
			if host.User != "" {
				target = host.User + "@" + target
			}
			if host.Port != 0 && host.Port != 22 {
				target += ":" + strconv.Itoa(host.Port)
			}
			if host.FromConfig {
				target += "   (ssh config)"
			}
			detail = target
		}
		mark := "  "
		markStyle := stPlain
		switch {
		case name == m.hostLabel():
			mark, markStyle = "● ", stOK
		case name == m.connecting:
			mark, markStyle = "◌ ", stWarn
			detail = "connecting…"
		}
		nameW := 28
		row := withBG(markStyle, sel).Render(mark) +
			cell(name, nameW, false, withBG(stBold, sel)) +
			cell(safeText(detail), max0(w-nameW-2), false, withBG(stMuted, sel))
		b.WriteString(pad(row, w, sel) + "\n")
	}

	if p.configErr != "" {
		b.WriteString(stFaint.Render(truncate(safeText(p.configErr), w)) + "\n")
	}
	if m.hostErr != "" {
		b.WriteString("\n" + stCrit.Render(truncate(safeText(m.hostErr), w)) + "\n")
	}
	if p.adding {
		label := "connect to: "
		if strings.HasPrefix(p.input, "@") {
			label = "ssh config: "
		}
		b.WriteString("\n" + stAccent.Render(label) + stPlain.Render(safeText(strings.TrimPrefix(p.input, "@"))) + stMuted.Render("█") + "\n")
	}

	b.WriteString("\n" + stFaint.Render(truncate(
		"Remote hosts are read over SSH with your keys or ssh-agent. Nothing is installed on them.", w)))
	return b.String()
}
