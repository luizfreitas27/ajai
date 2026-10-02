// Package ui is the terminal output: styled on a TTY, plain when piped, when
// NO_COLOR is set or when --no-color is given. Everything the commands print to
// the user goes through a Printer so tests can capture it.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// Printer writes user-facing text.
type Printer struct {
	Out    io.Writer
	Err    io.Writer
	styles styles
	color  bool
}

type styles struct {
	banner, ok, warn, fail, key, dim, title lipgloss.Style
}

// New builds a Printer. Colour is on only when out is a terminal, NO_COLOR is
// unset and noColor is false.
func New(out, err io.Writer, noColor bool) *Printer {
	color := !noColor && os.Getenv("NO_COLOR") == ""
	if f, ok := out.(*os.File); ok {
		color = color && term.IsTerminal(f.Fd())
	} else {
		color = false
	}
	r := lipgloss.NewRenderer(out)
	if !color {
		r.SetColorProfile(termenv.Ascii)
	}
	p := &Printer{Out: out, Err: err, color: color}
	p.styles = styles{
		banner: r.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED")),
		ok:     r.NewStyle().Bold(true).Foreground(lipgloss.Color("#16A34A")),
		warn:   r.NewStyle().Bold(true).Foreground(lipgloss.Color("#D97706")),
		fail:   r.NewStyle().Bold(true).Foreground(lipgloss.Color("#DC2626")),
		key:    r.NewStyle().Foreground(lipgloss.Color("#06B6D4")),
		dim:    r.NewStyle().Faint(true),
		title:  r.NewStyle().Bold(true),
	}
	return p
}

// Color reports whether styling is active.
func (p *Printer) Color() bool { return p.color }

// Banner prints the tool name and version once per command.
func (p *Printer) Banner(version string) {
	fmt.Fprintln(p.Out, p.styles.banner.Render("aja")+" "+p.styles.dim.Render(version))
}

// Title prints a section heading.
func (p *Printer) Title(text string) { fmt.Fprintln(p.Out, p.styles.title.Render(text)) }

// Printf writes plain text.
func (p *Printer) Printf(format string, args ...any) { fmt.Fprintf(p.Out, format, args...) }

// Println writes a plain line.
func (p *Printer) Println(args ...any) { fmt.Fprintln(p.Out, args...) }

// OK, Warn and Fail print a status line: a marker, a label, a detail.
func (p *Printer) OK(label, detail string)   { p.status(p.styles.ok.Render("ok  "), label, detail) }
func (p *Printer) Warn(label, detail string) { p.status(p.styles.warn.Render("warn"), label, detail) }
func (p *Printer) Fail(label, detail string) { p.status(p.styles.fail.Render("FAIL"), label, detail) }

func (p *Printer) status(marker, label, detail string) {
	fmt.Fprintf(p.Out, "%s  %-14s %s\n", marker, label, detail)
}

// KV prints an aligned key/value line.
func (p *Printer) KV(key, value string) {
	fmt.Fprintf(p.Out, "  %s %s\n", p.styles.key.Render(fmt.Sprintf("%-14s", key)), value)
}

// Error prints an error to Err, prefixed.
func (p *Printer) Error(err error) {
	fmt.Fprintln(p.Err, p.styles.fail.Render("error:")+" "+strings.TrimSpace(err.Error()))
}

// Hint prints a dim hint line.
func (p *Printer) Hint(text string) { fmt.Fprintln(p.Out, p.styles.dim.Render("  "+text)) }
