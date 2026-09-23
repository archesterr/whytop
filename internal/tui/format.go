package tui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

func bytesFmt(n float64) string {
	if n < 1024 {
		return strconv.Itoa(int(math.Round(n))) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	i := -1
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	// 1000-1023 of a unit would print four digits and overflow every
	// fixed-width column it lands in ("1002 MiB/s" is ten characters in a
	// nine-character cell). Roll it over: "1.0 GiB" says the same thing.
	if n >= 999.5 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if n < 10 {
		return fmt.Sprintf("%.1f %s", n, units[i])
	}
	return fmt.Sprintf("%.0f %s", n, units[i])
}

func rateFmt(n float64) string {
	if n < 1 {
		return "–"
	}
	return bytesFmt(n) + "/s"
}

func f1(n float64) string {
	if n < 0.05 {
		return "0"
	}
	return fmt.Sprintf("%.1f", n)
}

var unescRe = regexp.MustCompile(`\\x([0-9a-fA-F]{2})`)

func unitName(u string) string {
	return unescRe.ReplaceAllStringFunc(u, func(m string) string {
		var b byte
		fmt.Sscanf(m[2:], "%x", &b)
		return string(rune(b))
	})
}

func dur(d time.Duration) string {
	sec := int64(d.Seconds())
	if sec < 0 {
		sec = 0
	}
	days, h, m := sec/86400, sec%86400/3600, sec%3600/60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

func ago(t time.Time) string {
	return dur(time.Since(t))
}
