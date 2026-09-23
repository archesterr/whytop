module github.com/archesterr/whytop

// Go 1.26 is required by golang.org/x/sys v0.48.0 and x/text v0.42.0.
//
// It first came in with golang.org/x/crypto v0.56.0, which fixed eight
// advisories in the SSH client. The SSH client has since been removed —
// whytop watches only the machine it is installed on — and x/crypto with
// it, but x/sys and x/text had moved to 1.26 in the meantime, and holding
// them back means holding back the syscall and Unicode tables for no gain.
//
// The cost is still real: Debian trixie ships Go 1.24, so this is one of
// the two things blocking the archive route. See docs/PACKAGING.md.
go 1.26.0

toolchain go1.26.8

require (
	github.com/charmbracelet/bubbletea v1.3.10
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/muesli/termenv v0.16.0
	github.com/shirou/gopsutil/v4 v4.24.12
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.4.1 // indirect
	github.com/charmbracelet/x/ansi v0.11.6 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.15 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.9.0 // indirect
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.5.0 // indirect
	github.com/ebitengine/purego v0.8.1 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
