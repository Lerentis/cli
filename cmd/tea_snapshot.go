package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

type snapshotModel struct {
	taskModel
	state string
	items uint32
	edges uint32
}

type startSnapshotMsg struct {
	newState string
}
type progressSnapshotMsg struct {
	newState string
	items    uint32
	edges    uint32
}
type finishSnapshotMsg struct {
	newState string
	items    uint32
	edges    uint32
}

func NewSnapShotModel(title string) snapshotModel {
	return snapshotModel{
		taskModel: NewTaskModel(title),
		state:     "pending",
	}
}

func (m snapshotModel) Init() tea.Cmd {
	return m.taskModel.Init()
}

func (m snapshotModel) Update(msg tea.Msg) (snapshotModel, tea.Cmd) {
	switch msg := msg.(type) {
	case startSnapshotMsg:
		m.state = msg.newState
	case progressSnapshotMsg:
		m.state = msg.newState
		m.items = msg.items
		m.edges = msg.edges
	case finishSnapshotMsg:
		m.state = msg.newState
		m.items = msg.items
		m.edges = msg.edges
	default:
		var cmd tea.Cmd
		m.taskModel, cmd = m.taskModel.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m snapshotModel) View() string {
	// TODO: add spinner and/or progressbar; complication: we do not have a
	// expected number of items/edges to count towards for the progressbar
	if m.items == 0 && m.edges == 0 {
		return fmt.Sprintf("%v - %v", m.taskModel.View(), m.state)
	} else if m.items == 1 && m.edges == 0 {
		return fmt.Sprintf("%v - %v: 1 item", m.taskModel.View(), m.state)
	} else if m.items == 1 && m.edges == 1 {
		return fmt.Sprintf("%v - %v: 1 item, 1 edge", m.taskModel.View(), m.state)
	} else if m.items > 1 && m.edges == 0 {
		return fmt.Sprintf("%v - %v: %d items", m.taskModel.View(), m.state, m.items)
	} else if m.items > 1 && m.edges == 1 {
		return fmt.Sprintf("%v - %v: %d items, 1 edge", m.taskModel.View(), m.state, m.items)
	} else {
		return fmt.Sprintf("%v - %v: %d items, %d edges", m.taskModel.View(), m.state, m.items, m.edges)
	}
}
