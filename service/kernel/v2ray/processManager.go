package v2ray

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/common/resolv"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/iptables"
	"github.com/v2rayA/v2rayA/kernel/v2ray/asset"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

var (
	writeTransparentProxyRulesFn                 = writeTransparentProxyRules
	deleteTransparentProxyRulesFn                = deleteTransparentProxyRules
	deleteTransparentProxyRulesKeepSystemProxyFn = deleteTransparentProxyRulesKeepSystemProxy
)

type CoreProcessManager struct {
	// lifecycle serializes child replacement, transparent rules and teardown.
	// mu protects process and monitor state.
	lifecycle        sync.Mutex
	p                *Process
	retiring         *Process
	mu               sync.Mutex
	startGen         uint64
	testing          bool
	networkPaused    bool
	connectivityStop chan struct{}
}

var ProcessManager CoreProcessManager

func (m *CoreProcessManager) beforeStop(p *Process) {
	if p != nil && p.template != nil && p.template.Setting != nil {
		m.stopTransparentProxy(p.template.Setting)
	}

	if corehook := conf.GetEnvironmentConfig().CoreHook; corehook != "" {
		hook := strings.Split(corehook, " ")
		hook = append(hook, "--stage=pre-stop", fmt.Sprintf("--v2raya-confdir=%v", conf.GetEnvironmentConfig().Config))
		log.Info("Execute the core pre stop hook: %v", hook)
		b, err := exec.Command(hook[0], hook[1:]...).CombinedOutput()
		if len(b) > 0 {
			log.Info("Executing the core pre stop hook: %v", string(b))
		}
		if err != nil {
			log.Warn("Error when executing the core pre stop hook: %v", err)
			return
		}
	}
}

func (m *CoreProcessManager) GetRunningTemplate() *Template {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.p == nil {
		return nil
	}
	return m.p.template
}

func (m *CoreProcessManager) SetLatencyTesting(testing bool) {
	m.mu.Lock()
	m.testing = testing
	m.mu.Unlock()
}

// ServiceRunning reports whether the proxy service should be treated as running
// by the frontend. Temporary latency tests and connectivity-paused transparent
// proxy states are excluded from this view.
func (m *CoreProcessManager) ServiceRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.p != nil && !m.testing && !m.networkPaused
}

func (m *CoreProcessManager) NetworkPaused() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.networkPaused
}

func (m *CoreProcessManager) stopConnectivityMonitorLocked() {
	if m.connectivityStop != nil {
		close(m.connectivityStop)
		m.connectivityStop = nil
	}
}

func (m *CoreProcessManager) CheckAndSetupTransparentProxy(checkRunning bool, setting *configure.Setting, tmpl *Template) (err error) {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if checkRunning {
		current := m.GetRunningTemplate()
		if current == nil || tmpl != nil && tmpl != current {
			return nil
		}
		tmpl = current
	}
	return m.setupTransparentProxy(setting, tmpl)
}

func (m *CoreProcessManager) setupTransparentProxy(setting *configure.Setting, tmpl *Template) (err error) {
	if setting != nil {
		copy := *setting
		setting = &copy
		setting.FillEmpty()
	} else {
		setting = configure.GetSettingNotNil()
	}
	if IsTransparentOn(setting) {
		if setting.TransparentType == configure.TransparentSystemProxy {
			_ = iptables.SystemProxy.GetCleanCommands().Run(false)
		} else {
			deleteTransparentProxyRulesFn()
		}

		if thook := conf.GetEnvironmentConfig().TransparentHook; thook != "" {
			hook := strings.Split(thook, " ")
			hook = append(hook,
				fmt.Sprintf("--transparent-type=%v", setting.TransparentType),
				"--stage=pre-start",
				fmt.Sprintf("--v2raya-confdir=%v", conf.GetEnvironmentConfig().Config))
			log.Info("Execute the transparent pre start hook: %v", hook)
			b, err := exec.Command(hook[0], hook[1:]...).CombinedOutput()
			if len(b) > 0 {
				log.Info("Executing the transparent pre start hook: %v", string(b))
			}
			if err != nil {
				return fmt.Errorf("error when executing the transparent pre start hook: %w", err)
			}
		}

		if err = writeTransparentProxyRulesFn(tmpl); err != nil {
			return err
		}

		if thook := conf.GetEnvironmentConfig().TransparentHook; thook != "" {
			hook := strings.Split(thook, " ")
			hook = append(hook,
				fmt.Sprintf("--transparent-type=%v", setting.TransparentType),
				"--stage=post-start",
				fmt.Sprintf("--v2raya-confdir=%v", conf.GetEnvironmentConfig().Config))
			log.Info("Execute the transparent post start hook: %v", hook)
			b, err := exec.Command(hook[0], hook[1:]...).CombinedOutput()
			if len(b) > 0 {
				log.Info("Executing the transparent post start hook: %v", string(b))
			}
			if err != nil {
				return fmt.Errorf("error when executing the transparent post start hook: %w", err)
			}
		}
	}
	return nil
}

func (m *CoreProcessManager) CheckAndStopTransparentProxy(setting *configure.Setting) {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.stopTransparentProxy(setting)
}

func (m *CoreProcessManager) stopTransparentProxy(setting *configure.Setting) {
	if setting == nil {
		if t := m.GetRunningTemplate(); t != nil {
			setting = t.Setting
		} else {
			return
		}
	}
	if setting.Transparent != configure.TransparentClose {
		if thook := conf.GetEnvironmentConfig().TransparentHook; thook != "" {
			hook := strings.Split(thook, " ")
			hook = append(hook,
				fmt.Sprintf("--transparent-type=%v", setting.TransparentType),
				"--stage=pre-stop",
				fmt.Sprintf("--v2raya-confdir=%v", conf.GetEnvironmentConfig().Config))
			log.Info("Execute the transparent pre stop hook: %v", hook)
			b, err := exec.Command(hook[0], hook[1:]...).CombinedOutput()
			if len(b) > 0 {
				log.Info("Executing the transparent pre stop hook: %v", string(b))
			}
			if err != nil {
				log.Warn("Error when executing the transparent pre stop hook: %v", err)
			}
		}

		if setting.TransparentType == configure.TransparentSystemProxy {
			_ = iptables.SystemProxy.GetCleanCommands().Run(false)
		} else {
			deleteTransparentProxyRulesFn()
		}

		if thook := conf.GetEnvironmentConfig().TransparentHook; thook != "" {
			hook := strings.Split(thook, " ")
			hook = append(hook,
				fmt.Sprintf("--transparent-type=%v", setting.TransparentType),
				"--stage=post-stop",
				fmt.Sprintf("--v2raya-confdir=%v", conf.GetEnvironmentConfig().Config))
			log.Info("Execute the transparent post stop hook: %v", hook)
			b, err := exec.Command(hook[0], hook[1:]...).CombinedOutput()
			if len(b) > 0 {
				log.Info("Executing the transparent post stop hook: %v", string(b))
			}
			if err != nil {
				log.Warn("Error when executing the transparent post stop hook: %v", err)
				return
			}
		}
	}
}

func (m *CoreProcessManager) afterStop(p *Process) {
	if corehook := conf.GetEnvironmentConfig().CoreHook; corehook != "" {
		hook := strings.Split(corehook, " ")
		hook = append(hook, "--stage=post-stop", fmt.Sprintf("--v2raya-confdir=%v", conf.GetEnvironmentConfig().Config))
		log.Info("Execute the core post stop hook: %v", hook)
		b, err := exec.Command(hook[0], hook[1:]...).CombinedOutput()
		if len(b) > 0 {
			log.Info("Executing the core post stop hook: %v", string(b))
		}
		if err != nil {
			log.Warn("Error when executing the core post stop hook: %v", err)
			return
		}
	}
}

func (m *CoreProcessManager) Stop(saveRunning bool) {
	m.stopCurrent(saveRunning)
}

// Return the exact child that received the stop request so a caller can
// await its exit after releasing the manager lock.
func (m *CoreProcessManager) stopCurrent(saveRunning bool) *Process {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	p := m.stop(saveRunning)
	if p == nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.retiring
	}
	status := configure.LastKernelExitRunning
	if saveRunning {
		status = configure.LastKernelExitStopped
	}
	_ = configure.SetLastKernelExitStatus(status)
	return p
}

// Caller holds lifecycle. Publish the stopped state before running teardown
// hooks so status readers remain available while a hook or child is exiting.
func (m *CoreProcessManager) stop(saveRunning bool) *Process {
	m.mu.Lock()
	p := m.p
	if p == nil {
		m.mu.Unlock()
		return nil
	}
	m.startGen++
	m.stopConnectivityMonitorLocked()
	m.networkPaused = false
	m.p = nil
	m.retiring = p
	m.mu.Unlock()

	m.beforeStop(p)
	if err := p.Close(); err != nil {
		log.Warn("CoreProcessManager.Stop: %v", err)
	}
	if saveRunning {
		configure.SetRunning(false)
	}
	m.afterStop(p)
	ApiFeed.ProductMessage("running_state", map[string]interface{}{"running": false, "networkPaused": false})
	return p
}

func (m *CoreProcessManager) handleUnexpectedStop(p *Process) {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if m.Process() != p {
		return
	}
	m.stop(true)
	_ = configure.SetLastKernelExitStatus(configure.LastKernelExitCrashed)
}

// runPreStartHook executes the configured core pre-start hook.
// It is called inside the Start lock so it is correctly ordered after the
// pre-stop hook that runs inside m.stop.
func (m *CoreProcessManager) runPreStartHook() error {
	if corehook := conf.GetEnvironmentConfig().CoreHook; corehook != "" {
		hook := strings.Split(corehook, " ")
		hook = append(hook, "--stage=pre-start", fmt.Sprintf("--v2raya-confdir=%v", conf.GetEnvironmentConfig().Config))
		log.Info("Execute the core pre start hook: %v", hook)
		b, err := exec.Command(hook[0], hook[1:]...).CombinedOutput()
		if len(b) > 0 {
			log.Info("Executing the core pre start hook: %v", string(b))
		}
		if err != nil {
			return fmt.Errorf("error when executing the core pre start hook: %w", err)
		}
	}
	return nil
}

func (m *CoreProcessManager) afterStart(t *Template, gen uint64, process *Process) (err error) {
	if err := m.setupTransparentProxy(t.Setting, t); err != nil {
		return err
	}
	m.startConnectivityMonitor(t, gen, process)

	if corehook := conf.GetEnvironmentConfig().CoreHook; corehook != "" {
		hook := strings.Split(corehook, " ")
		hook = append(hook, "--stage=post-start", fmt.Sprintf("--v2raya-confdir=%v", conf.GetEnvironmentConfig().Config))
		log.Info("Execute the core post start hook: %v", hook)
		b, err := exec.Command(hook[0], hook[1:]...).CombinedOutput()
		if len(b) > 0 {
			log.Info("Executing the core post start hook: %v", string(b))
		}
		if err != nil {
			return fmt.Errorf("error when executing the core post start hook: %w", err)
		}
	}
	return nil
}

func (m *CoreProcessManager) Start(t *Template) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if !shouldLocalDnsListen(t.Setting) {
		resolv.CheckResolvConf()
	}
	if (t.Setting.Transparent == configure.TransparentGfwlist || t.Setting.RulePortMode == configure.GfwlistMode) && !asset.DoesV2rayAssetExist("LoyalsoldierSite.dat") {
		_ = t.Close()
		return fmt.Errorf("cannot find GFWList files. update GFWList and try again")
	}

	m.stop(true)
	m.mu.Lock()
	oldProc := m.retiring
	m.startGen++
	gen := m.startGen
	m.mu.Unlock()

	// Wait briefly for the previous process to exit before launching NewProcess
	// so non-SO_REUSEPORT listeners (such as the DNS module) are released first.
	if oldProc != nil {
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if waitErr := oldProc.WaitUntilExit(waitCtx); waitErr != nil {
			cancel()
			_ = t.Close()
			return fmt.Errorf("previous core did not exit before restart: %w", waitErr)
		}
		cancel()
		m.mu.Lock()
		m.retiring = nil
		m.mu.Unlock()
	}

	process, err := NewProcess(t, m.runPreStartHook, func() error { return nil }, m.handleUnexpectedStop)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.p = process
	m.mu.Unlock()
	if err := m.afterStart(t, gen, process); err != nil {
		m.stop(true)
		return err
	}
	m.mu.Lock()
	testing := m.testing
	m.mu.Unlock()
	configure.SetRunning(true)
	_ = configure.SetLastKernelExitStatus(configure.LastKernelExitRunning)
	if !testing {
		ApiFeed.ProductMessage("running_state", map[string]interface{}{"running": true, "networkPaused": false})
	}
	return nil
}

// Running reports if v2ray-core is running.
func (m *CoreProcessManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.p != nil
}

func (m *CoreProcessManager) Process() *Process {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.p
}
