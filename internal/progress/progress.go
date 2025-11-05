package progress

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// EventType represents the type of build event
type EventType string

const (
	EventTypeStart      EventType = "start"
	EventTypeProgress   EventType = "progress"
	EventTypeStageStart EventType = "stage_start"
	EventTypeStageDone  EventType = "stage_done"
	EventTypeLog        EventType = "log"
	EventTypeSuccess    EventType = "success"
	EventTypeError      EventType = "error"
)

// Stage represents a build stage
type Stage string

const (
	StageValidation  Stage = "validation"
	StageDownload    Stage = "download"
	StageExtract     Stage = "extract"
	StageBuild       Stage = "build"
	StagePackage     Stage = "package"
	StageCompress    Stage = "compress"
	StageFinalize    Stage = "finalize"
)

// Event represents a structured build progress event
type Event struct {
	Type      EventType `json:"type"`
	Stage     Stage     `json:"stage,omitempty"`
	Message   string    `json:"message"`
	Progress  int       `json:"progress,omitempty"`  // 0-100
	Total     int64     `json:"total,omitempty"`     // Total bytes/items
	Current   int64     `json:"current,omitempty"`   // Current bytes/items
	Timestamp time.Time `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Tracker tracks build progress and emits events
type Tracker struct {
	mu        sync.RWMutex
	writers   []io.Writer
	currentStage Stage
	startTime time.Time
	lastEvent time.Time
}

// NewTracker creates a new progress tracker
func NewTracker() *Tracker {
	return &Tracker{
		writers:   make([]io.Writer, 0),
		startTime: time.Now(),
		lastEvent: time.Now(),
	}
}

// AddWriter adds a writer to receive events (e.g., SSE stream, CLI output)
func (t *Tracker) AddWriter(w io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writers = append(t.writers, w)
}

// CurrentStage returns the current build stage
func (t *Tracker) CurrentStage() Stage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.currentStage
}

// Emit sends an event to all registered writers
func (t *Tracker) Emit(event Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	event.Timestamp = time.Now()
	t.lastEvent = event.Timestamp

	data, _ := json.Marshal(event)

	for _, w := range t.writers {
		// SSE format: "data: <json>\n\n"
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		if f, ok := w.(flusher); ok {
			f.Flush()
		}
	}
}

type flusher interface {
	Flush()
}

// Start emits a build start event
func (t *Tracker) Start(strategy string) {
	t.Emit(Event{
		Type:    EventTypeStart,
		Message: fmt.Sprintf("Starting %s build", strategy),
		Metadata: map[string]interface{}{
			"strategy": strategy,
		},
	})
}

// StartStage marks the beginning of a build stage
func (t *Tracker) StartStage(stage Stage, message string) {
	t.mu.Lock()
	t.currentStage = stage
	t.mu.Unlock()

	t.Emit(Event{
		Type:    EventTypeStageStart,
		Stage:   stage,
		Message: message,
	})
}

// UpdateStage updates progress within the current stage
func (t *Tracker) UpdateStage(message string, progress int, current, total int64) {
	t.mu.RLock()
	stage := t.currentStage
	t.mu.RUnlock()

	t.Emit(Event{
		Type:     EventTypeProgress,
		Stage:    stage,
		Message:  message,
		Progress: progress,
		Current:  current,
		Total:    total,
	})
}

// CompleteStage marks a stage as complete
func (t *Tracker) CompleteStage(message string) {
	t.mu.RLock()
	stage := t.currentStage
	t.mu.RUnlock()

	t.Emit(Event{
		Type:     EventTypeStageDone,
		Stage:    stage,
		Message:  message,
		Progress: 100,
	})
}

// Log emits a log message (for important info that should always be shown)
func (t *Tracker) Log(message string) {
	t.Emit(Event{
		Type:    EventTypeLog,
		Message: message,
	})
}

// Success emits a success event
func (t *Tracker) Success(output string, size int64) {
	elapsed := time.Since(t.startTime)
	t.Emit(Event{
		Type:     EventTypeSuccess,
		Message:  fmt.Sprintf("Build completed in %s", formatDuration(elapsed)),
		Progress: 100,
		Metadata: map[string]interface{}{
			"output":  output,
			"size":    size,
			"elapsed": elapsed.Seconds(),
		},
	})
}

// Error emits an error event
func (t *Tracker) Error(err error, stage Stage) {
	t.Emit(Event{
		Type:    EventTypeError,
		Stage:   stage,
		Message: err.Error(),
	})
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}

// FormatBytes formats bytes in a human-readable way
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}
