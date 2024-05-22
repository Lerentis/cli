package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"connectrpc.com/connect"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/overmindtech/cli/tracing"
	"github.com/overmindtech/sdp-go"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// terraformApplyCmd represents the `terraform apply` command
var terraformApplyCmd = &cobra.Command{
	Use:   "apply [overmind options...] -- [terraform options...]",
	Short: "Runs `terraform apply` between two full system configuration snapshots for tracking. This will be automatically connected with the Change created by the `plan` command.",
	PreRun: func(cmd *cobra.Command, args []string) {
		// Bind these to viper
		err := viper.BindPFlags(cmd.Flags())
		if err != nil {
			log.WithError(err).Fatal("could not bind `terraform apply` flags")
		}
	},
	Run: CmdWrapper("apply", []string{"explore:read", "changes:write", "config:write", "request:receive"}, NewTfApplyModel),
}

type tfApplyModel struct {
	ctx context.Context // note that this ctx is not initialized on NewTfPlanModel to instead get a modified context through the loadSourcesConfigMsg that has a timeout and cancelFunction configured
	oi  OvermindInstance

	args []string

	planFile    string
	needPlan    bool
	runPlanTask runPlanModel

	runPlanFinished       bool
	revlinkWarmupFinished bool

	submitPlanTask submitPlanModel

	processingHeader string
	needApproval     bool

	changeUuid             uuid.UUID
	isStarting             bool
	startingChange         chan tea.Msg
	startingChangeSnapshot snapshotModel
	runTfApply             bool
	isEnding               bool
	endingChange           chan tea.Msg
	endingChangeSnapshot   snapshotModel
	progress               []string

	width int
}

type startStartingSnapshotMsg struct{}

type changeIdentifiedMsg struct {
	uuid uuid.UUID
}

type runTfApplyMsg struct{}
type tfApplyFinishedMsg struct{}

func NewTfApplyModel(args []string) tea.Model {
	hasPlanSet := false
	autoapprove := false
	planFile := "overmind.plan"
	if len(args) >= 1 {
		f, err := os.Stat(args[len(args)-1])
		if err == nil && !f.IsDir() {
			// the last argument is a file, check that the previous arg is not
			// one that would eat this as argument
			hasPlanSet = true
			if len(args) >= 2 {
				prev := args[len(args)-2]
				for _, a := range []string{"-backup", "--backup", "-state", "--state", "-state-out", "--state-out"} {
					if prev == a || strings.HasPrefix(prev, a+"=") {
						hasPlanSet = false
						break
					}
				}
			}
		}
		if hasPlanSet {
			planFile = args[len(args)-1]
			autoapprove = true
		}
	}

	planArgs := append([]string{"plan"}, planArgsFromApplyArgs(args)...)

	if !hasPlanSet {
		// if the user has not set a plan, we need to set a temporary file to
		// capture the output for all calculations and to run apply afterwards

		f, err := os.CreateTemp("", "overmind-plan")
		if err != nil {
			log.WithError(err).Fatal("failed to create temporary plan file")
		}

		planFile = f.Name()

		planArgs = append(planArgs, "-out", planFile)
		args = append(args, planFile)

		// auto
		for _, a := range args {
			if a == "-auto-approve" || a == "-auto-approve=true" || a == "-auto-approve=TRUE" || a == "--auto-approve" || a == "--auto-approve=true" || a == "--auto-approve=TRUE" {
				autoapprove = true
			}
			if a == "-auto-approve=false" || a == "-auto-approve=FALSE" || a == "--auto-approve=false" || a == "--auto-approve=FALSE" {
				autoapprove = false
			}
		}
	}

	args = append([]string{"apply"}, args...)

	processingHeader := `# Applying Changes

Applying changes with ` + "`" + `terraform %v` + "`\n"
	processingHeader = fmt.Sprintf(processingHeader, strings.Join(args, " "))

	return tfApplyModel{
		args: args,

		planFile:        planFile,
		needPlan:        !hasPlanSet,
		runPlanTask:     NewRunPlanModel(planArgs, planFile),
		runPlanFinished: hasPlanSet,

		submitPlanTask: NewSubmitPlanModel(planFile),

		processingHeader: processingHeader,
		needApproval:     !autoapprove,

		startingChange:         make(chan tea.Msg, 10), // provide a small buffer for sending updates, so we don't block the processing
		startingChangeSnapshot: NewSnapShotModel("Starting Change"),
		endingChange:           make(chan tea.Msg, 10), // provide a small buffer for sending updates, so we don't block the processing
		endingChangeSnapshot:   NewSnapShotModel("Ending Change"),
		progress:               []string{},
	}
}

func (m tfApplyModel) Init() tea.Cmd {
	cmds := []tea.Cmd{}

	if m.needPlan {
		cmds = append(
			cmds,
			m.runPlanTask.Init(),
			m.submitPlanTask.Init(),
		)
	}

	return tea.Batch(cmds...)
}

func (m tfApplyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width

	case loadSourcesConfigMsg:
		m.ctx = msg.ctx
		m.oi = msg.oi

	case revlinkWarmupFinishedMsg:
		m.revlinkWarmupFinished = true
		if m.runPlanFinished {
			cmds = append(cmds, func() tea.Msg {
				if m.needPlan {
					return submitPlanNowMsg{}
				} else {
					return startStartingSnapshotMsg{}
				}
			})
		}
	case runPlanFinishedMsg:
		m.runPlanFinished = true
		if m.revlinkWarmupFinished {
			cmds = append(cmds, func() tea.Msg {
				if m.needPlan {
					return submitPlanNowMsg{}
				} else {
					return startStartingSnapshotMsg{}
				}
			})
		}

	case submitPlanFinishedMsg:
		cmds = append(cmds, func() tea.Msg { return startStartingSnapshotMsg{} })

	case startStartingSnapshotMsg:
		m.isStarting = true
		cmds = append(cmds,
			m.startingChangeSnapshot.Init(),
			m.startStartChangeCmd(),
			m.waitForStartingActivity,
		)

	case changeIdentifiedMsg:
		m.changeUuid = msg.uuid
		cmds = append(cmds, m.waitForStartingActivity)

	case startSnapshotMsg:
		if msg.id == m.startingChangeSnapshot.spinner.ID() {
			cmds = append(cmds, m.waitForStartingActivity)
		} else if msg.id == m.endingChangeSnapshot.spinner.ID() {
			cmds = append(cmds, m.waitForEndingActivity)
		}

	case progressSnapshotMsg:
		if msg.id == m.startingChangeSnapshot.spinner.ID() {
			cmds = append(cmds, m.waitForStartingActivity)
		} else if msg.id == m.endingChangeSnapshot.spinner.ID() {
			cmds = append(cmds, m.waitForEndingActivity)
		}

	case finishSnapshotMsg:
		if msg.id == m.startingChangeSnapshot.spinner.ID() {
			m.isStarting = false
			// defer the actual command to give the view a chance to show the header
			m.runTfApply = true
			cmds = append(cmds, func() tea.Msg { return runTfApplyMsg{} })
		} else if msg.id == m.endingChangeSnapshot.spinner.ID() {
			cmds = append(cmds, func() tea.Msg { return delayQuitMsg{} })
		}

	case runTfApplyMsg:
		c := exec.CommandContext(m.ctx, "terraform", m.args...) // nolint:gosec // this is a user-provided command, let them do their thing

		// inject the profile, if configured
		if aws_profile := viper.GetString("aws-profile"); aws_profile != "" {
			c.Env = append(c.Env, fmt.Sprintf("AWS_PROFILE=%v", aws_profile))
		}

		_, span := tracing.Tracer().Start(m.ctx, "terraform apply", trace.WithAttributes(
			attribute.String("command", strings.Join(m.args, " ")),
		))
		return m, tea.ExecProcess(
			c,
			func(err error) tea.Msg {
				defer span.End()

				if err != nil {
					return fatalError{err: fmt.Errorf("failed to run terraform apply: %w", err)}
				}

				return tfApplyFinishedMsg{}
			})
	case tfApplyFinishedMsg:
		m.isEnding = true
		cmds = append(cmds,
			m.endingChangeSnapshot.Init(),
			m.startEndChangeCmd(),
			m.waitForEndingActivity,
		)
	}

	mdl, cmd := m.startingChangeSnapshot.Update(msg)
	cmds = append(cmds, cmd)
	m.startingChangeSnapshot = mdl

	mdl, cmd = m.endingChangeSnapshot.Update(msg)
	cmds = append(cmds, cmd)
	m.endingChangeSnapshot = mdl

	if m.needPlan {
		mdl, cmd := m.runPlanTask.Update(msg)
		cmds = append(cmds, cmd)
		m.runPlanTask = mdl.(runPlanModel)

		mdl, cmd = m.submitPlanTask.Update(msg)
		cmds = append(cmds, cmd)
		m.submitPlanTask = mdl.(submitPlanModel)
	}

	return m, tea.Batch(cmds...)
}

func (m tfApplyModel) View() string {
	bits := []string{}

	if m.runPlanTask.status != taskStatusPending {
		bits = append(bits, m.runPlanTask.View())
	}

	if m.submitPlanTask.Status() != taskStatusPending {
		bits = append(bits, m.submitPlanTask.View())
	}

	if m.isStarting || m.runTfApply || m.isEnding {
		bits = append(bits, markdownToString(m.processingHeader))

		if m.startingChangeSnapshot.status != taskStatusPending {
			bits = append(bits, m.startingChangeSnapshot.View())
		}

		if m.endingChangeSnapshot.status != taskStatusPending {
			bits = append(bits, m.endingChangeSnapshot.View())
		}

		bits = append(bits, strings.Join(m.progress, "\n"))
	}

	return strings.Join(bits, "\n") + "\n"
}

func (m tfApplyModel) startStartChangeCmd() tea.Cmd {
	ctx := m.ctx
	oi := m.oi

	return func() tea.Msg {
		var err error
		ticketLink := viper.GetString("ticket-link")
		if ticketLink == "" {
			ticketLink, err = getTicketLinkFromPlan(m.planFile)
			if err != nil {
				return fatalError{err: err}
			}
		}

		changeUuid, err := getChangeUuid(ctx, oi, sdp.ChangeStatus_CHANGE_STATUS_DEFINING, ticketLink, true)
		if err != nil {
			return fatalError{err: fmt.Errorf("failed to identify change: %w", err)}
		}

		m.startingChange <- changeIdentifiedMsg{uuid: changeUuid}
		m.startingChange <- m.startingChangeSnapshot.StartMsg("starting")

		client := AuthenticatedChangesClient(ctx, oi)
		startStream, err := client.StartChange(ctx, &connect.Request[sdp.StartChangeRequest]{
			Msg: &sdp.StartChangeRequest{
				ChangeUUID: changeUuid[:],
			},
		})
		if err != nil {
			return fatalError{err: fmt.Errorf("failed to start change: %w", err)}
		}

		var msg *sdp.StartChangeResponse
		for startStream.Receive() {
			msg = startStream.Msg()
			log.WithFields(log.Fields{
				"state": msg.GetState(),
				"items": msg.GetNumItems(),
				"edges": msg.GetNumEdges(),
			}).Trace("progress")
			m.startingChange <- m.startingChangeSnapshot.ProgressMsg(msg.GetState().String(), msg.GetNumItems(), msg.GetNumEdges())
		}
		if startStream.Err() != nil {
			return fatalError{err: fmt.Errorf("failed to process start change: %w", startStream.Err())}
		}

		return m.startingChangeSnapshot.FinishMsg(msg.GetState().String(), msg.GetNumItems(), msg.GetNumEdges())
	}
}

// A command that waits for the activity on the startingChange channel.
func (m tfApplyModel) waitForStartingActivity() tea.Msg {
	return <-m.startingChange
}

func (m tfApplyModel) startEndChangeCmd() tea.Cmd {
	ctx := m.ctx
	oi := m.oi
	changeUuid := m.changeUuid

	return func() tea.Msg {
		m.endingChange <- m.endingChangeSnapshot.StartMsg("ending")

		client := AuthenticatedChangesClient(ctx, oi)
		endStream, err := client.EndChange(ctx, &connect.Request[sdp.EndChangeRequest]{
			Msg: &sdp.EndChangeRequest{
				ChangeUUID: changeUuid[:],
			},
		})
		if err != nil {
			return fatalError{err: fmt.Errorf("failed to end change: %w", err)}
		}

		var msg *sdp.EndChangeResponse
		for endStream.Receive() {
			msg = endStream.Msg()
			log.WithFields(log.Fields{
				"state": msg.GetState(),
				"items": msg.GetNumItems(),
				"edges": msg.GetNumEdges(),
			}).Trace("progress")
			m.endingChange <- m.endingChangeSnapshot.ProgressMsg(msg.GetState().String(), msg.GetNumItems(), msg.GetNumEdges())
		}
		if endStream.Err() != nil {
			return fatalError{err: fmt.Errorf("failed to process end change: %w", endStream.Err())}
		}

		return m.endingChangeSnapshot.FinishMsg(msg.GetState().String(), msg.GetNumItems(), msg.GetNumEdges())
	}
}

// A command that waits for the activity on the endingChange channel.
func (m tfApplyModel) waitForEndingActivity() tea.Msg {
	return <-m.endingChange
}

func init() {
	terraformCmd.AddCommand(terraformApplyCmd)

	addAPIFlags(terraformApplyCmd)
	addChangeUuidFlags(terraformApplyCmd)
	addTerraformBaseFlags(terraformApplyCmd)
}
