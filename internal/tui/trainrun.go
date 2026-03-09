package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/sairus2k/glmt/internal/train"
)

// StepStatus represents the status of a single step in the train run.
type StepStatus int

const (
	StepPending StepStatus = iota
	StepRunning
	StepDone
	StepFailed
	StepSkipped
)

// StepEntry represents a single step in the processing of a merge request.
type StepEntry struct {
	Name      string
	Status    StepStatus
	Message   string
	MRIID     int
	Timestamp time.Time
	StartedAt time.Time
}

// MRStepLog holds the step log for a single merge request.
type MRStepLog struct {
	Steps []StepEntry
}

// TrainRunModel is the bubbletea model for the train run screen.
// It shows live progress of the merge train execution.
type TrainRunModel struct {
	mrs          []*gitlab.MergeRequest
	mrSteps      []MRStepLog
	logEntries   []StepEntry
	currentMR    int
	done         bool
	aborted      bool
	result       *train.Result
	startTime    time.Time
	spinnerFrame int
}

// Messages used by the train run screen.

type trainStepMsg struct {
	mrIID   int
	step    string
	message string
}

type trainDoneMsg struct {
	result *train.Result
}

type trainAbortMsg struct{}

type trainBackMsg struct{}

// NewTrainRunModel creates a new TrainRunModel with the given merge requests.
func NewTrainRunModel(mrs []*gitlab.MergeRequest) TrainRunModel {
	mrSteps := make([]MRStepLog, len(mrs))
	for i := range mrSteps {
		mrSteps[i] = MRStepLog{}
	}
	return TrainRunModel{
		mrs:       mrs,
		mrSteps:   mrSteps,
		currentMR: 0,
		startTime: time.Now(),
	}
}

// Init returns the initial command for the model.
func (m TrainRunModel) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates model state.
func (m TrainRunModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		if !m.done {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, spinnerTick()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	case trainStepMsg:
		return m.handleStep(msg)
	case trainDoneMsg:
		m.done = true
		m.result = msg.result
		return m, nil
	}
	return m, nil
}

func (m TrainRunModel) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.done || m.aborted {
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc", "enter":
			return m, func() tea.Msg { return trainBackMsg{} }
		}
		return m, nil
	}
	switch key {
	case "q", "esc", "ctrl+c":
		m.aborted = true
		return m, func() tea.Msg { return trainAbortMsg{} }
	}
	return m, nil
}

func (m TrainRunModel) handleStep(msg trainStepMsg) (tea.Model, tea.Cmd) {
	now := time.Now()

	// Find the MR index by IID (mrIID=0 means main pipeline steps)
	mrIdx := -1
	targetBranch := ""
	for i, mr := range m.mrs {
		if mr.IID == msg.mrIID {
			mrIdx = i
			targetBranch = mr.TargetBranch
			break
		}
	}

	// For MR-specific steps, require a matching MR
	if msg.mrIID != 0 && mrIdx < 0 {
		return m, nil
	}

	// Use first MR's target branch for main pipeline steps
	if targetBranch == "" && len(m.mrs) > 0 {
		targetBranch = m.mrs[0].TargetBranch
	}

	// Update currentMR to reflect the MR being processed
	if mrIdx >= 0 && mrIdx > m.currentMR {
		m.currentMR = mrIdx
	}

	entry := StepEntry{
		Name:      mapStepName(msg.step, targetBranch, msg.message),
		Status:    mapStepStatus(msg.step),
		Message:   msg.message,
		MRIID:     msg.mrIID,
		Timestamp: now,
		StartedAt: now,
	}

	// Mark all previously running steps as done (processing is sequential)
	for i := range m.logEntries {
		if m.logEntries[i].Status == StepRunning {
			m.logEntries[i].Status = StepDone
		}
	}
	for mi := range m.mrSteps {
		for si := range m.mrSteps[mi].Steps {
			if m.mrSteps[mi].Steps[si].Status == StepRunning {
				m.mrSteps[mi].Steps[si].Status = StepDone
			}
		}
	}

	// Append to per-MR steps (for backward compat)
	if mrIdx >= 0 {
		m.mrSteps[mrIdx].Steps = append(m.mrSteps[mrIdx].Steps, entry)
	}

	// Always append to chronological log
	m.logEntries = append(m.logEntries, entry)

	return m, nil
}

// mapStepName converts internal step identifiers to display names.
func mapStepName(step, targetBranch, message string) string {
	switch step {
	case "rebase":
		return fmt.Sprintf("Rebase onto %s", targetBranch)
	case "pipeline_wait":
		return "Pipeline running"
	case "pipeline_success":
		return "Pipeline passed"
	case "pipeline_failed":
		return "Pipeline failed"
	case "merge":
		return "Merged"
	case "merge_sha_mismatch":
		return "SHA mismatch — retrying"
	case "skip":
		return fmt.Sprintf("Skipped: %s", message)
	case "cancel_main_pipeline":
		return "Main pipeline cancelled"
	case "main_pipeline_wait":
		return "Main pipeline running"
	case "main_pipeline_done":
		return fmt.Sprintf("Main pipeline %s", message)
	case "restart_pipeline":
		return "Restart cancelled pipeline"
	default:
		return step
	}
}

// mapStepStatus converts a step identifier to its status.
func mapStepStatus(step string) StepStatus {
	switch step {
	case "pipeline_wait", "main_pipeline_wait":
		return StepRunning
	case "rebase", "pipeline_success", "merge", "cancel_main_pipeline", "main_pipeline_done", "restart_pipeline":
		return StepDone
	case "pipeline_failed":
		return StepFailed
	case "skip":
		return StepSkipped
	case "merge_sha_mismatch":
		return StepRunning
	default:
		return StepPending
	}
}

// View renders the train run screen.
func (m TrainRunModel) View() tea.View {
	var b strings.Builder

	// Header
	total := len(m.mrs)
	if m.done {
		b.WriteString("  ")
		b.WriteString(sSuccess.Styled(fmt.Sprintf("✓ Finished processing %d MRs", total)))
		b.WriteString("\n\n")
	} else if m.aborted {
		b.WriteString("  ")
		b.WriteString(sError.Styled(fmt.Sprintf("✗ Aborted — processed %d of %d MRs", m.currentMR, total)))
		b.WriteString("\n\n")
	} else {
		spinner := spinnerFrames[m.spinnerFrame]
		b.WriteString("  ")
		b.WriteString(sRunning.Styled(spinner))
		b.WriteString(" ")
		b.WriteString(sHeader.Styled(fmt.Sprintf("Merging %d of %d MRs", m.currentMR+1, total)))
		b.WriteString("\n\n")
	}

	// MR legend line
	var legendParts []string
	for _, mr := range m.mrs {
		legendParts = append(legendParts, fmt.Sprintf("!%d %s", mr.IID, mr.Title))
	}
	b.WriteString("  ")
	b.WriteString(sFaint.Styled(strings.Join(legendParts, "  ·  ")))
	b.WriteString("\n\n")

	// Chronological log entries
	for i, entry := range m.logEntries {
		isLast := i == len(m.logEntries)-1

		// Timestamp
		ts := entry.Timestamp.Format("15:04:05")
		b.WriteString("  ")
		b.WriteString(sFaint.Styled(ts))
		b.WriteString("  ")

		// Status icon
		icon := m.styledStepIcon(entry.Status)
		b.WriteString(icon)
		b.WriteString("  ")

		// MR reference (if MR-specific)
		if entry.MRIID != 0 {
			b.WriteString(sBold.Styled(fmt.Sprintf("!%d", entry.MRIID)))
			b.WriteString("  ")
		}

		// Step name
		b.WriteString(entry.Name)

		// Elapsed time for running entries (only the last one)
		if entry.Status == StepRunning && isLast && !entry.StartedAt.IsZero() {
			b.WriteString("  ")
			b.WriteString(sFaint.Styled(formatDuration(time.Since(entry.StartedAt))))
		}

		b.WriteString("\n")
	}

	return tea.NewView(b.String())
}

func (m TrainRunModel) styledStepIcon(status StepStatus) string {
	switch status {
	case StepDone:
		return sSuccess.Styled("✓")
	case StepRunning:
		return sRunning.Styled(spinnerFrames[m.spinnerFrame])
	case StepFailed:
		return sError.Styled("✗")
	case StepSkipped:
		return sWarning.Styled("⚠")
	default:
		return " "
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// Exported getters for testing.

// CurrentMR returns the index of the MR currently being processed.
func (m TrainRunModel) CurrentMR() int { return m.currentMR }

// Done returns whether the train run has completed.
func (m TrainRunModel) Done() bool { return m.done }

// Aborted returns whether the train run was aborted by the user.
func (m TrainRunModel) Aborted() bool { return m.aborted }

// MRSteps returns the step logs for all MRs.
func (m TrainRunModel) MRSteps() []MRStepLog { return m.mrSteps }

// LogEntries returns the chronological log entries.
func (m TrainRunModel) LogEntries() []StepEntry { return m.logEntries }

// Result returns the train execution result.
func (m TrainRunModel) Result() *train.Result { return m.result }

// KeyHints returns the keyboard hints for the train run screen.
func (m TrainRunModel) KeyHints() []KeyHint {
	if m.done || m.aborted {
		return []KeyHint{{"[Enter]", "back"}, {"[q]", "quit"}}
	}
	return []KeyHint{{"[Esc]", "abort"}}
}
