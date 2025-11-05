package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// CLIRenderer renders progress events as beautiful terminal output
type CLIRenderer struct {
	mu           sync.Mutex
	tracker      *Tracker
	currentStage Stage
	stageStart   time.Time
	termWidth    int
	isTTY        bool
}

// NewCLIRenderer creates a new CLI progress renderer
func NewCLIRenderer(tracker *Tracker) *CLIRenderer {
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}

	renderer := &CLIRenderer{
		tracker:   tracker,
		termWidth: width,
		isTTY:     term.IsTerminal(int(os.Stdout.Fd())),
	}

	tracker.AddWriter(renderer)
	return renderer
}

// Write implements io.Writer to receive progress events
func (r *CLIRenderer) Write(p []byte) (n int, err error) {
	// Events are written as SSE format: "data: <json>\n\n"
	// We need to parse and render them
	return len(p), nil
}

// Flush implements flusher interface
func (r *CLIRenderer) Flush() {}

// RenderEvent renders a single event to the terminal
func (r *CLIRenderer) RenderEvent(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch event.Type {
	case EventTypeStart:
		r.printHeader("🚀 Fledge Build Engine", event.Message)

	case EventTypeStageStart:
		r.currentStage = event.Stage
		r.stageStart = time.Now()
		icon := r.getStageIcon(event.Stage)
		fmt.Printf("\n%s %s\n", icon, styleStage(event.Message))

	case EventTypeProgress:
		r.renderProgressBar(event)

	case EventTypeStageDone:
		elapsed := time.Since(r.stageStart)
		icon := "✓"
		fmt.Printf("\r%s %s %s\n",
			styleSuccess(icon),
			event.Message,
			styleDim(fmt.Sprintf("(%s)", formatDuration(elapsed))))

	case EventTypeLog:
		fmt.Printf("  %s\n", event.Message)

	case EventTypeSuccess:
		r.printSuccess(event)

	case EventTypeError:
		r.printError(event)
	}
}

// renderProgressBar renders a modern progress bar
func (r *CLIRenderer) renderProgressBar(event Event) {
	if !r.isTTY {
		// Non-TTY: just print the message
		fmt.Printf("  %s\n", event.Message)
		return
	}

	// Terminal width minus prefix/suffix spacing
	barWidth := r.termWidth - 50
	if barWidth < 20 {
		barWidth = 20
	}

	filled := int(float64(barWidth) * float64(event.Progress) / 100.0)
	if filled > barWidth {
		filled = barWidth
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	var suffix string
	if event.Total > 0 && event.Current > 0 {
		suffix = fmt.Sprintf("%s / %s",
			FormatBytes(event.Current),
			FormatBytes(event.Total))
	} else {
		suffix = fmt.Sprintf("%d%%", event.Progress)
	}

	// Clear line and render
	fmt.Printf("\r  %s %s %s",
		styleBar(bar),
		styleDim(event.Message),
		stylePercent(suffix))
}

// printHeader prints a styled header
func (r *CLIRenderer) printHeader(title, subtitle string) {
	border := strings.Repeat("─", r.termWidth-4)
	fmt.Printf("\n┌─%s─┐\n", border)
	fmt.Printf("│ %s %s│\n",
		styleBold(title),
		strings.Repeat(" ", r.termWidth-len(title)-6))
	if subtitle != "" {
		fmt.Printf("│ %s %s│\n",
			styleDim(subtitle),
			strings.Repeat(" ", r.termWidth-len(subtitle)-6))
	}
	fmt.Printf("└─%s─┘\n", border)
}

// printSuccess prints a success message
func (r *CLIRenderer) printSuccess(event Event) {
	fmt.Printf("\n")
	fmt.Printf("┌─%s─┐\n", strings.Repeat("─", r.termWidth-4))
	fmt.Printf("│ %s %s\n",
		styleSuccess("✓ "+event.Message),
		strings.Repeat(" ", r.termWidth-len(event.Message)-6))

	if meta, ok := event.Metadata["output"].(string); ok {
		fmt.Printf("│ %s %s\n",
			styleDim("Output: "),
			meta)
	}
	if meta, ok := event.Metadata["size"].(int64); ok {
		fmt.Printf("│ %s %s\n",
			styleDim("Size:   "),
			FormatBytes(meta))
	}
	fmt.Printf("└─%s─┘\n\n", strings.Repeat("─", r.termWidth-4))
}

// printError prints an error message
func (r *CLIRenderer) printError(event Event) {
	fmt.Printf("\n")
	fmt.Printf("┌─%s─┐\n", strings.Repeat("─", r.termWidth-4))
	fmt.Printf("│ %s Build Failed\n", styleError("✗"))
	fmt.Printf("│ %s\n", event.Message)
	fmt.Printf("└─%s─┘\n\n", strings.Repeat("─", r.termWidth-4))
}

// getStageIcon returns an icon for each build stage
func (r *CLIRenderer) getStageIcon(stage Stage) string {
	switch stage {
	case StageValidation:
		return "🔍"
	case StageDownload:
		return "📥"
	case StageExtract:
		return "📦"
	case StageBuild:
		return "🔨"
	case StagePackage:
		return "📦"
	case StageCompress:
		return "🗜️ "
	case StageFinalize:
		return "✨"
	default:
		return "▶ "
	}
}

// ANSI styling functions
func styleBold(s string) string {
	return fmt.Sprintf("\033[1m%s\033[0m", s)
}

func styleDim(s string) string {
	return fmt.Sprintf("\033[2m%s\033[0m", s)
}

func styleSuccess(s string) string {
	return fmt.Sprintf("\033[32m%s\033[0m", s)
}

func styleError(s string) string {
	return fmt.Sprintf("\033[31m%s\033[0m", s)
}

func styleStage(s string) string {
	return fmt.Sprintf("\033[36m%s\033[0m", s)
}

func styleBar(s string) string {
	return fmt.Sprintf("\033[35m%s\033[0m", s)
}

func stylePercent(s string) string {
	return fmt.Sprintf("\033[33m%s\033[0m", s)
}
