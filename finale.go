package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// statRow is one labeled line in the done-screen session stats.
type statRow struct {
	label string
	value string
}

// bannerArt is the pixel wordmark traced from the reference image.
// Lines are padded to one width so centering cannot shear the letters.
const bannerArt = `                                       ███         ███        
         ███                           ███         ███        
   ████  ███        ██  ██ ██████    █████  ████   ███  ████  
 ██  ███ ███        ██  ██ ███████ ███████ ██  ███ ███ ███ ███
   █████ ███ █████  ██  ██ ███ ███ ███ ███  ██████ ███ ███ ███
 ███ ███ ███        ██  ██ ███ ███ ███ ███ ███ ███ ███ █████  
 ███ ███ ███        ██████ ███████ ███████ ███ ███ ███ ███    
█  █████████      ██ ███ ████████ ███ █████ ███████████ ██████
                           ███                                
                           ██                                 `

var wideBanner = strings.Split(padBlock(bannerArt), "\n")

var wideBannerWidth = lipgloss.Width(wideBanner[0])

// Gradient stops run left to right. Light is the same sweep, darkened for a light terminal.
var (
	darkStops = []rgb{
		mustHex("22D3EE"), mustHex("818CF8"), mustHex("A78BFA"),
		mustHex("E879F9"), mustHex("FBBF24"), mustHex("4ADE80"),
	}
	lightStops = []rgb{
		mustHex("0E7490"), mustHex("4338CA"), mustHex("6D28D9"),
		mustHex("A21CAF"), mustHex("A16207"), mustHex("15803D"),
	}
)

type rgb struct{ r, g, b uint8 }

// mustHex parses a fixed RGB palette entry and rejects malformed constants.
func mustHex(s string) rgb {
	var r, g, b int
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b); err != nil {
		panic(err)
	}
	return rgb{uint8(r), uint8(g), uint8(b)}
}

// hex formats a color for Lip Gloss without losing leading zeroes.
func (c rgb) hex() string {
	return fmt.Sprintf("#%02X%02X%02X", c.r, c.g, c.b)
}

// sampleRGB interpolates between palette stops, clamping at the endpoints.
func sampleRGB(stops []rgb, t float64) rgb {
	if t <= 0 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	pos := t * float64(len(stops)-1)
	i := int(pos)
	f := pos - float64(i)
	a, b := stops[i], stops[i+1]
	lerp := func(x, y uint8) uint8 {
		return uint8(float64(x) + (float64(y)-float64(x))*f)
	}
	return rgb{lerp(a.r, b.r), lerp(a.g, b.g), lerp(a.b, b.b)}
}

// paintBannerLine colors ink with a left-to-right gradient that drifts
// slightly down the rows.
func paintBannerLine(s string, row, rows int) string {
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) * 8)
	span := max(rows-1, 1)
	for col, r := range runes {
		if r == ' ' {
			b.WriteRune(' ')
			continue
		}
		t := 0.0
		if n > 1 {
			t = float64(col) / float64(n-1)
		}
		t += (float64(row)/float64(span) - 0.5) * 0.18
		t = min(max(t, 0), 1)
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
			Light: sampleRGB(lightStops, t).hex(),
			Dark:  sampleRGB(darkStops, t).hex(),
		}).Render(string(r)))
	}
	return b.String()
}

// padBlock right-pads every line to the widest line so the block stays aligned.
func padBlock(s string) string {
	lines := strings.Split(s, "\n")
	w := 0
	for _, line := range lines {
		w = max(w, lipgloss.Width(line))
	}
	for i, line := range lines {
		lines[i] = pad(line, w)
	}
	return strings.Join(lines, "\n")
}

// bannerLines reserves space for statistics before choosing wide or compact art.
// Very short viewports omit the artwork so result counts appear first.
func bannerLines(width, height, statsHeight int) []string {
	if width >= wideBannerWidth && len(wideBanner)+1+statsHeight <= height {
		return wideBanner
	}
	const name = "ai-update"
	if 2+1+statsHeight <= height {
		return []string{name, strings.Repeat("─", len(name))}
	}
	return nil
}

// sessionPoster is the done-screen output: the name, then this run's stats.
func (m model) sessionPoster() string {
	width := max(m.vp.Width, 1)
	// Wrap before padding so long names cannot make the centered block too wide.
	stats := padBlock(ansi.Wrap(m.sessionStats(), width, ""))
	lines := bannerLines(width, m.vp.Height, lipgloss.Height(stats))
	painted := make([]string, len(lines))
	for i, line := range lines {
		painted[i] = paintBannerLine(line, i, len(lines))
	}
	// Pad stats to a rectangle first. PlaceHorizontal centers each line on its
	// own, which would slide a short row sideways and break the columns.
	out := lipgloss.PlaceHorizontal(width, lipgloss.Center, stats)
	if len(lines) > 0 {
		art := lipgloss.PlaceHorizontal(width, lipgloss.Center, strings.Join(painted, "\n"))
		out = art + "\n\n" + out
	}
	if m.log != nil && m.log.path != "" {
		out += "\n\n" + lipgloss.NewStyle().Foreground(muted).Width(width).Render("log  "+m.log.path)
	}
	return out
}

// sessionStats summarizes selected tools, failures, and the slowest completed job.
func (m model) sessionStats() string {
	var updated, current, failed int
	var failedNames []string
	var slow *job
	for _, i := range m.run {
		j := m.jobs[i]
		switch j.status {
		case stUpdated:
			updated++
		case stCurrent:
			current++
		case stFailed:
			failed++
			failedNames = append(failedNames, j.tool.Name)
		}
		if slow == nil || j.dur > slow.dur {
			slow = j
		}
	}

	greenSt := lipgloss.NewStyle().Foreground(green)
	redSt := lipgloss.NewStyle().Foreground(red)
	count := func(n int, on lipgloss.Style) string {
		s := fmt.Sprintf("%d", n)
		if n == 0 {
			return mutedSt.Render(s)
		}
		return on.Render(s)
	}

	rows := []statRow{
		{"tools", boldSt.Render(fmt.Sprintf("%d", len(m.run)))},
		{"elapsed", boldSt.Render(fmtDur(m.elapsed))},
		{"updated", count(updated, greenSt)},
		{"already latest", mutedSt.Render(fmt.Sprintf("%d", current))},
		{"failed", count(failed, redSt)},
	}
	if slow != nil && slow.dur > 0 && len(m.run) > 1 {
		rows = append(rows, statRow{"slowest", fmt.Sprintf("%s  %s", slow.tool.Name, fmtDur(slow.dur))})
	}
	if len(failedNames) > 0 {
		rows = append(rows, statRow{"failed tools", redSt.Render(strings.Join(failedNames, ", "))})
	}

	labelW := 0
	for _, r := range rows {
		labelW = max(labelW, lipgloss.Width(r.label))
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s  %s", mutedSt.Render(pad(r.label, labelW)), r.value)
	}
	return b.String()
}
