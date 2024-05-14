package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/ansi"
)

// waitForCancellation returns a tea.Cmd that will wait for SIGINT and SIGTERM and run the provided cancel on receipt.
func waitForCancellation(ctx context.Context, cancel context.CancelFunc) tea.Cmd {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	return func() tea.Msg {
		select {
		case <-sigs:
			cancel()
		case <-ctx.Done():
		}
		return tea.Quit
	}
}

// fatalError is a wrapper for errors that should abort the running tea.Program.
type fatalError struct {
	id  int
	err error
}

// otherError is a wrapper for errors that should NOT abort the running tea.Program.
type otherError struct {
	id  int
	err error
}

type taskStatus int

const (
	taskStatusPending taskStatus = 0
	taskStatusRunning taskStatus = 1
	taskStatusDone    taskStatus = 2
	taskStatusError   taskStatus = 3
	taskStatusSkipped taskStatus = 4
)

type taskModel struct {
	status  taskStatus
	title   string
	spinner spinner.Model
}

type WithTaskModel interface {
	TaskModel() taskModel
}

// assert that taskModel implements WithTaskModel
var _ WithTaskModel = (*taskModel)(nil)

func NewTaskModel(title string) taskModel {
	return taskModel{
		status: taskStatusPending,
		title:  title,
		spinner: spinner.New(
			spinner.WithSpinner(spinner.Moon),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color(ColorPalette.Light.BgMain))),
		),
	}
}

func (m taskModel) Init() tea.Cmd {
	if m.status == taskStatusRunning {
		return m.spinner.Tick
	}
	return nil
}

func (m taskModel) TaskModel() taskModel {
	return m
}

func (m taskModel) Update(msg tea.Msg) (taskModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" {
			return m, tea.Quit
		}
	default:
		if m.status == taskStatusRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

func (m taskModel) View() string {
	switch m.status {
	case taskStatusPending:
		return fmt.Sprintf("⏳ %v", m.title)
	case taskStatusRunning:
		v := m.spinner.View()
		switch ansi.PrintableRuneWidth(v) {
		case 0:
			v = "  "
		case 1:
			v += " "
		}
		return fmt.Sprintf("%v %v", m.spinner.View(), m.title)
	case taskStatusDone:
		return fmt.Sprintf("✅ %v", m.title)
	case taskStatusError:
		return fmt.Sprintf("⛔️ %v", m.title)
	case taskStatusSkipped:
		return fmt.Sprintf("-- %v", m.title)
	default:
		return fmt.Sprintf("❓ %v", m.title)
	}
}
