package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/emdash-ai/glmt/internal/gitlab"
	"github.com/emdash-ai/glmt/internal/train"
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
	StartedAt time.Time
}

// MRStepLog holds the step log for a single merge request.
type MRStepLog struct {
	Steps []StepEntry
}

// TrainRunModel is the bubbletea model for the train run screen.
// It shows live progress of the merge train execution.
type TrainRunModel struct {
	mrs       []*gitlab.MergeRequest
	mrSteps   []MRStepLog
	currentMR int
	done      bool
	aborted   bool
	result    *train.Result
	startTime time.Time
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
	switch key {
	case "q", "ctrl+c":
		m.aborted = true
		return m, func() tea.Msg { return trainAbortMsg{} }
	}
	return m, nil
}

func (m TrainRunModel) handleStep(msg trainStepMsg) (tea.Model, tea.Cmd) {
	// Find the MR index by IID
	mrIdx := -1
	for i, mr := range m.mrs {
		if mr.IID == msg.mrIID {
			mrIdx = i
			break
		}
	}
	if mrIdx < 0 {
		return m, nil
	}

	// Update currentMR to reflect the MR being processed
	if mrIdx > m.currentMR {
		m.currentMR = mrIdx
	}

	entry := StepEntry{
		Name:      mapStepName(msg.step, m.mrs[mrIdx].TargetBranch, msg.message),
		Status:    mapStepStatus(msg.step),
		Message:   msg.message,
		StartedAt: time.Now(),
	}

	m.mrSteps[mrIdx].Steps = append(m.mrSteps[mrIdx].Steps, entry)
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
	default:
		return step
	}
}

// mapStepStatus converts a step identifier to its status.
func mapStepStatus(step string) StepStatus {
	switch step {
	case "pipeline_wait", "main_pipeline_wait":
		return StepRunning
	case "rebase", "pipeline_success", "merge", "cancel_main_pipeline", "main_pipeline_done":
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
		fmt.Fprintf(&b, "\n  Finished processing %d MRs\n\n", total)
	} else if m.aborted {
		fmt.Fprintf(&b, "\n  Aborted — processed %d of %d MRs\n\n", m.currentMR, total)
	} else {
		currentIID := 0
		if m.currentMR < len(m.mrs) {
			currentIID = m.mrs[m.currentMR].IID
		}
		fmt.Fprintf(&b, "\n  Merging %d of %d MRs · !%d in progress\n\n", m.currentMR+1, total, currentIID)
	}

	// Per-MR blocks
	for i, mr := range m.mrs {
		steps := m.mrSteps[i].Steps
		isSkipped := false
		for _, s := range steps {
			if s.Status == StepSkipped {
				isSkipped = true
				break
			}
		}

		// MR header line
		if isSkipped {
			fmt.Fprintf(&b, "  !%d  %s  SKIPPED\n", mr.IID, mr.Title)
		} else {
			fmt.Fprintf(&b, "  !%d  %s\n", mr.IID, mr.Title)
		}

		// Step lines
		for j, step := range steps {
			isLast := j == len(steps)-1
			connector := "├─"
			if isLast {
				connector = "└─"
			}

			icon := stepIcon(step.Status)

			if step.Message != "" {
				fmt.Fprintf(&b, "  %s %s %s    %s\n", connector, icon, step.Name, step.Message)
			} else {
				fmt.Fprintf(&b, "  %s %s %s\n", connector, icon, step.Name)
			}
		}

		b.WriteString("\n")
	}

	// Footer
	if !m.done {
		b.WriteString("  [q] abort\n")
	}

	return tea.NewView(b.String())
}

func stepIcon(status StepStatus) string {
	switch status {
	case StepDone:
		return "✓"
	case StepRunning:
		return "…"
	case StepFailed:
		return "✗"
	case StepSkipped:
		return "⚠"
	default:
		return " "
	}
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

// Result returns the train execution result.
func (m TrainRunModel) Result() *train.Result { return m.result }
