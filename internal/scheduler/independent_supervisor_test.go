package scheduler

import (
	"context"
	"testing"

	"github.com/Kardbrd/kardbrd-agent/internal/rules"
	"github.com/robfig/cron/v3"
)

func TestIndependentSupervisorFailedReloadPreservesSchedules(t *testing.T) {
	old := rules.Schedule{Name: "old", Cron: "0 1 * * *", Action: "old action"}
	m := NewManager([]rules.Schedule{old}, "board1", nil, nil)
	m.cron = cron.New(cron.WithParser(m.parser))
	m.ctx = context.Background()
	if err := m.installSchedulesLocked(m.ctx); err != nil {
		t.Fatal(err)
	}
	oldID := m.entries[0]
	err := m.UpdateSchedules([]rules.Schedule{{Name: "new", Cron: "0 2 * * *", Action: "new action"}, {Name: "invalid", Cron: "invalid cron", Action: "bad"}})
	if err == nil {
		t.Fatal("invalid schedule accepted")
	}
	if len(m.Schedules) != 1 || m.Schedules[0].Name != "old" || len(m.entries) != 1 || m.entries[0] != oldID {
		t.Fatal("failed reload replaced old schedules with partial candidate")
	}
}
