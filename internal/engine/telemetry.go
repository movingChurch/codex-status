package engine

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/movingChurch/runtime-status/internal/telemetry"
)

type telemetrySession struct{ reader *telemetry.SessionReader }

func (s *telemetrySession) read() map[string]any { return s.reader.Read() }
func (s *telemetrySession) reset()               { s.reader.ResetAfterAccountChange() }
func (s *telemetrySession) invalidated() bool    { return s.reader.Invalidated }
func (s *telemetrySession) bytesRead() int64     { return s.reader.BytesRead }
func productionDependencies() dependencies {
	// Preserve the exact-profile startup observation once per auth generation.
	// Subsequent quotas come from the native session; unresolved accounts never
	// create a speculative periodic polling loop or merge across profile roots.
	return dependencies{epoch: telemetry.AuthEpoch, bind: telemetry.BindRun, bindStatus: telemetry.BindRunStatus, reader: func(path, id string) session { return &telemetrySession{telemetry.NewSessionReader(path, id)} }, visible: paneVisibility, account: telemetry.FetchAccount, accountOnce: true, links: telemetry.LinkSnapshot}
}
func renderSnapshot(snapshot map[string]any, width int, color bool) []string {
	return telemetry.Render(snapshot, width, color)
}
func paneVisibility(socket string) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "tmux", "-S", socket, "list-panes", "-a", "-F", "#{pane_id}\t#{window_active}\t#{session_attached}\t#{window_zoomed_flag}\t#{pane_active}\t#{pane_dead}").Output()
	if err != nil {
		return nil
	}
	return parseVisibility(string(raw))
}
func parseVisibility(raw string) map[string]bool {
	visible := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		row := strings.Split(line, "\t")
		if len(row) != 6 {
			continue
		}
		attached, err := strconv.Atoi(row[2])
		visible[row[0]] = row[1] == "1" && err == nil && attached > 0 && row[5] == "0" && (row[3] != "1" || row[4] == "1")
	}
	return visible
}
