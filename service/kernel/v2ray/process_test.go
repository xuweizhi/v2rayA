package v2ray

import (
	"github.com/v2rayA/v2rayA/db/configure"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessCloseMarksExpectedStopBeforeCancel(t *testing.T) {
	process := &Process{template: &Template{}}
	process.procCancel = func() {
		if !process.expectedStop.Load() {
			t.Fatal("expected stop must be recorded before canceling the core")
		}
	}

	if err := process.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !process.expectedStop.Load() {
		t.Fatal("Close() did not record the expected stop")
	}
}

func TestRecoveryProbeCannotResumeStoppedGeneration(t *testing.T) {
	origProbe, origWrite, origDelete := connectivityProbeForSetting, writeTransparentProxyRulesFn, deleteTransparentProxyRulesFn
	defer func() {
		connectivityProbeForSetting, writeTransparentProxyRulesFn, deleteTransparentProxyRulesFn = origProbe, origWrite, origDelete
	}()
	running, exitStatus := configure.GetRunning(), configure.GetLastKernelExitStatus()
	defer func() { configure.SetRunning(running); _ = configure.SetLastKernelExitStatus(exitStatus) }()
	started, release := make(chan struct{}), make(chan struct{})
	connectivityProbeForSetting = func(*Template) bool { close(started); <-release; return true }
	var writes atomic.Int32
	writeTransparentProxyRulesFn = func(*Template) error { writes.Add(1); return nil }
	deleteTransparentProxyRulesFn = func() {}
	tmpl := &Template{Setting: &configure.Setting{Transparent: configure.TransparentProxy, TransparentType: configure.TransparentTun}}
	p := &Process{template: tmpl}
	m := &CoreProcessManager{p: p, startGen: 1, networkPaused: true}
	done := make(chan bool, 1)
	go func() { stop, _ := m.syncConnectivityStateForGen(tmpl, 1, p); done <- stop }()
	<-started
	m.Stop(true)
	close(release)
	select {
	case stop := <-done:
		if !stop || writes.Load() != 0 || m.ServiceRunning() {
			t.Fatal("a completed probe resumed stale transparent rules")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovery remained blocked after stop")
	}
}
