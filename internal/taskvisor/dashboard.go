package taskvisor

import (
	"fmt"
	"io"
	"math"
	"time"
)

const (
	ansiClearScreen = "\033[2J\033[H"
	ansiReset       = "\033[0m"
	ansiGreen       = "\033[32m"
	ansiRed         = "\033[31m"
	ansiYellow      = "\033[33m"
	ansiDim         = "\033[2m"
	ansiBold        = "\033[1m"
	ansiCyan        = "\033[36m"
)

func formatElapsed(startedAt, finishedAt string) string {
	if startedAt == "" {
		return "—"
	}

	start, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return "—"
	}

	var elapsed time.Duration
	if finishedAt == "" {
		elapsed = time.Since(start)
	} else {
		finish, err := time.Parse(time.RFC3339, finishedAt)
		if err != nil {
			return "—"
		}
		elapsed = finish.Sub(start)
	}

	totalSeconds := int(math.Round(elapsed.Seconds()))
	if totalSeconds < 0 {
		totalSeconds = 0
	}

	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func goalStatusColor(status string) string {
	switch status {
	case GoalDone:
		return ansiGreen
	case GoalFailed:
		return ansiRed
	case GoalRunning:
		return ansiYellow
	case GoalPending:
		return ansiDim
	default:
		return ""
	}
}

func (d *Daemon) renderDashboard(w io.Writer) error {
	if _, err := fmt.Fprint(w, ansiClearScreen); err != nil {
		return err
	}

	if d.mode == modeIdle {
		if _, err := fmt.Fprintf(w, "%s%sTASKVISOR%s  %sIDLE%s — waiting for start signal\n",
			ansiBold, ansiCyan, ansiReset, ansiDim, ansiReset); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Poll interval: %s\n", d.pollInterval); err != nil {
			return err
		}
		return nil
	}

	phaseName := "NONE"
	switch d.runtime(d.currentGoal).phase {
	case phaseSupervising:
		phaseName = "SUPERVISING"
	case phaseValidating:
		phaseName = "VALIDATING"
	}

	if _, err := fmt.Fprintf(w, "%s%sTASKVISOR%s  %s%sACTIVE%s / %s%s%s\n",
		ansiBold, ansiCyan, ansiReset,
		ansiBold, ansiGreen, ansiReset,
		ansiBold, phaseName, ansiReset); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Poll: %s  Dispatch timeout: %s  Validate timeout: %s\n\n",
		d.pollInterval, d.dispatchTimeout, d.validateTimeout); err != nil {
		return err
	}

	goals, err := LoadGoals(d.workDir)
	if err != nil || goals == nil || len(goals.Goals) == 0 {
		return nil
	}

	if _, err := fmt.Fprintf(w, "%s%-4s  %-12s  %-30s  %-10s  %-8s  %s%s\n",
		ansiDim, "#", "ID", "Description", "Status", "Retries", "Elapsed", ansiReset); err != nil {
		return err
	}

	for i, g := range goals.Goals {
		color := goalStatusColor(g.Status)
		elapsed := formatElapsed(g.StartedAt, g.FinishedAt)

		desc := g.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}

		current := " "
		if g.ID == goals.CurrentGoal {
			current = ">"
		}

		if _, err := fmt.Fprintf(w, "%s%s%-3d  %-12s  %-30s  %-10s  %d/%-6d  %s%s\n",
			color, current, i+1, g.ID, desc, g.Status, g.Retries, g.MaxRetries, elapsed, ansiReset); err != nil {
			return err
		}
	}

	return nil
}
