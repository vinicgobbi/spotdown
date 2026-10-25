package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#5A3FC0", Dark: "#A78BFA"}
	colorOK     = lipgloss.AdaptiveColor{Light: "#1F7A3A", Dark: "#4ADE80"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#A15C00", Dark: "#FBBF24"}
	colorErr    = lipgloss.AdaptiveColor{Light: "#B42318", Dark: "#F87171"}
	colorInfo   = lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#60A5FA"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B8FA3"}
	colorBorder = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#3F3F50"}

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#FFFFFF"}).
			Background(colorAccent).Padding(0, 1)
	subtleStyle  = lipgloss.NewStyle().Foreground(colorMuted)
	accentStyle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	okStyle      = lipgloss.NewStyle().Foreground(colorOK)
	warnStyle    = lipgloss.NewStyle().Foreground(colorWarn)
	errStyle     = lipgloss.NewStyle().Foreground(colorErr)
	infoStyle    = lipgloss.NewStyle().Foreground(colorInfo)
	boldStyle    = lipgloss.NewStyle().Bold(true)
	cursorStyle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	sectionStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder).Padding(0, 1)
	keyStyle     = lipgloss.NewStyle().Foreground(colorAccent)
)

// help monta a linha de atalhos: pares tecla/descrição.
func help(pairs ...string) string {
	out := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		if out != "" {
			out += subtleStyle.Render("  •  ")
		}
		out += keyStyle.Render(pairs[i]) + " " + subtleStyle.Render(pairs[i+1])
	}
	return out
}
