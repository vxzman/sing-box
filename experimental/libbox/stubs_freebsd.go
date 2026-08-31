//go:build freebsd

package libbox

import (
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/service/oomkiller"
	"github.com/sagernet/sing-box/service/powerreport"
)

type oomReporter struct {
	startedService *daemon.StartedService
}

var _ oomkiller.OOMReporter = (*oomReporter)(nil)

func NewOOMReporter(startedService *daemon.StartedService) oomkiller.OOMReporter {
	return &oomReporter{startedService: startedService}
}

func (r *oomReporter) WriteReport(memoryUsage uint64) error {
	// OOM profiling is not supported on FreeBSD yet.
	return nil
}

func (r *oomReporter) WriteDraft(memoryUsage uint64) error {
	return nil
}

func (r *oomReporter) DiscardDraft() error {
	return nil
}

func PromoteOOMDraft() {
}

func PowerReportOptions(startedService *daemon.StartedService) powerreport.Options {
	// Power report is not supported on FreeBSD yet.
	return powerreport.Options{}
}

func PromotePowerReportDraft() {
}
