package utils

import (
	"fmt"
	"sync"
	"time"

	"github.com/pterm/pterm"
)

// TUIRenderer renders progress events in a dynamic way using a spinner
// and a live-updating text area.
type TUIRenderer struct {
	mu sync.Mutex

	spinner *pterm.SpinnerPrinter
	area    *pterm.AreaPrinter

	// state, updated by events
	lastEvents []ProgressEvent
	startTime  time.Time
}

// NewTUIRenderer creates a new TUI renderer instance.
func NewTUIRenderer() *TUIRenderer {
	return &TUIRenderer{
		lastEvents: make([]ProgressEvent, 0),
		startTime:  time.Now(),
	}
}

// Start initializes spinner + area. Call this once before you pass
// TUIRenderer.Sink() to WaitForResourcesReadySequential.
func (r *TUIRenderer) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.spinner == nil {
		spinner, err := pterm.DefaultSpinner.
			WithRemoveWhenDone(false).
			Start("Initializing...")
		if err != nil {
			return err
		}
		r.spinner = spinner
	}

	if r.area == nil {
		// Area is a better fit for “live text” than LivePrinter in newer pterm versions.
		area := &pterm.DefaultArea
		// Initialize with empty content
		area, _ = area.Start("")
		r.area = area
	}

	return nil
}

// Stop finalizes the spinner and area.
func (r *TUIRenderer) Stop(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	msg := "All resources became Ready"
	if err != nil {
		msg = fmt.Sprintf("Failed: %v", err)
	}
	if r.spinner != nil {
		if err != nil {
			r.spinner.Fail(msg)
		} else {
			r.spinner.Success(msg)
		}
	}

	if r.area != nil {
		_ = r.area.Stop()
	}
}

// Sink implements ProgressSink and can be passed directly to
// WaitForResourcesReadySequential.
func (r *TUIRenderer) Sink(ev ProgressEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update local state with latest event for the given resource index
	updated := false
	for i, e := range r.lastEvents {
		if e.CurrentIndex == ev.CurrentIndex && e.KindDescription == ev.KindDescription {
			r.lastEvents[i] = ev
			updated = true
			break
		}
	}
	if !updated {
		r.lastEvents = append(r.lastEvents, ev)
	}

	// Update spinner text
	if r.spinner != nil {
		base := ev.Message
		if ev.Err != nil {
			base = fmt.Sprintf("Error on %s", ev.KindDescription)
		}
		r.spinner.UpdateText(fmt.Sprintf("[%.0f%%] %s", ev.OverallPercent, base))
	}

	// Re-render table of current state
	if r.area != nil {
		r.renderTableLocked()
	}
}

// renderTableLocked must be called with r.mu held.
func (r *TUIRenderer) renderTableLocked() {
	if len(r.lastEvents) == 0 {
		return
	}

	header := []string{"#", "Kind", "Resource", "Status", "Progress", "Message"}
	// header := []string{"#", "Kind", "Namespace", "Name", "Resource", "Status", "Progress", "Message"}
	rows := [][]string{header}

	for _, ev := range r.lastEvents {
		status := "waiting"
		if ev.ResourceCompleted {
			status = "ready"
		}
		if ev.Err != nil {
			status = "error"
		}

		row := []string{
			fmt.Sprintf("%d/%d", ev.CurrentIndex, ev.Total),
			ev.KindDescription,
			// ev.Namespace,
			// ev.Name,
			ev.GVR.Resource,
			status,
			fmt.Sprintf("%.0f%%", ev.OverallPercent),
			ev.Message,
		}
		rows = append(rows, row)
	}

	table := pterm.DefaultTable.WithHasHeader().WithData(rows)
	content, _ := table.Srender()

	r.area.Update(content)
}