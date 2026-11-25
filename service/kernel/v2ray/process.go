package v2ray

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/mem"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/kernel/v2ray/asset"
	"github.com/v2rayA/v2rayA/kernel/v2ray/where"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

var NoConnectedServerErr = fmt.Errorf("no selected servers")

// Process is a v2ray-core process
type Process struct {
	// mutex protect the proc
	mutex               sync.Mutex
	proc                *os.Process
	cmd                 *exec.Cmd
	procCancel          func() // cancel func for proc
	template            *Template
	tag2WhichIndex      map[string]int
	groupTag2WhichIndex map[string]int
	done                chan struct{}
	expectedStop        atomic.Bool
}

func (p *Process) lookupWhichIndex(outboundName, outboundTag string) (int, bool) {
	if p == nil {
		return 0, false
	}
	if p.groupTag2WhichIndex != nil && outboundName != "" {
		if idx, ok := p.groupTag2WhichIndex[outboundName+"\x00"+outboundTag]; ok {
			return idx, true
		}
	}
	if p.tag2WhichIndex != nil {
		if idx, ok := p.tag2WhichIndex[outboundTag]; ok {
			return idx, true
		}
	}
	return 0, false
}

func NewProcess(tmpl *Template,
	prestart func() error, poststart func() error,
	postUnexpectedStop func(p *Process),
) (_ *Process, err error) {
	process := &Process{
		template: tmpl,
		done:     make(chan struct{}),
	}
	var rollbackStage int
	defer func() {
		if err != nil {
			process.rollback(rollbackStage)
			_ = tmpl.Close()
		}
	}()

	// DNS 模块由 v2raya-core 进程内启动（基于 dns_module 配置段），
	// v2rayA 仅负责生成配置和在透明代理时应用防火墙规则。
	if tmpl.Setting != nil && tmpl.DnsModuleConfig != nil {
		log.Info("DNS module config written to v2raya-core config")
	}
	if tmpl.MultiObservatory != nil {
		// NOTICE: tag2WhichIndex / groupTag2WhichIndex are reliable because once connected servers
		// are changed when v2ray is running, UpdateV2RayConfig is invoked and they are regenerated.
		tag2WhichIndex := make(map[string]int, len(tmpl.OutboundTags))
		groupTag2WhichIndex := make(map[string]int, len(tmpl.OutboundTags))
		for i, tag := range tmpl.OutboundTags {
			tag2WhichIndex[tag] = i
			if i < len(tmpl.outboundNames) && tmpl.outboundNames[i] != "" {
				groupTag2WhichIndex[tmpl.outboundNames[i]+"\x00"+tag] = i
			}
		}
		process.tag2WhichIndex = tag2WhichIndex
		process.groupTag2WhichIndex = groupTag2WhichIndex
	}
	err = WriteV2rayConfig(tmpl.ToConfigBytes())
	if err != nil {
		return nil, err
	}
	if err = tmpl.CheckInboundPortsOccupied(); err != nil {
		return nil, fmt.Errorf("%v", err)
	}
	pCtx, cancel := context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			cancel()
		}
	}()
	if tmpl.API == nil {
		return nil, fmt.Errorf("missing core API configuration")
	}
	process.procCancel = cancel
	if err = prestart(); err != nil {
		return nil, err
	}
	if err := tmpl.startPluginManagers(pCtx); err != nil {
		return nil, err
	}
	cmd, lw, err := startCoreProcessCmd(pCtx)
	if err != nil {
		return nil, err
	}
	process.cmd = cmd
	process.proc = cmd.Process
	rollbackStage = 1 // xray 已启动
	if err = poststart(); err != nil {
		// Kill and reap immediately so no zombie or os/exec pipe FD is leaked.
		process.expectedStop.Store(true)
		process.procCancel = nil
		cancel()
		_ = cmd.Wait()
		lw.Flush()
		close(process.done)
		rollbackStage = 0
		return nil, err
	}

	const (
		startupStateStarting    = int32(0)
		startupStateRunning     = int32(1)
		startupStateExitedEarly = int32(2)
	)
	var startupState atomic.Int32
	go func() {
		waitErr := cmd.Wait()
		cancel()
		lw.Flush()
		exitedBeforeReady := startupState.CompareAndSwap(startupStateStarting, startupStateExitedEarly)
		close(process.done)
		if process.expectedStop.Load() {
			// canceled by v2rayA
			return
		}
		var t []string
		if cmd.ProcessState != nil {
			if cmd.ProcessState.Success() {
				if exitedBeforeReady {
					log.Warn("v2ray-core: exited with status 0 during startup")
				}
				if !exitedBeforeReady && postUnexpectedStop != nil {
					postUnexpectedStop(process)
				}
				return
			}
			t = append(t, cmd.ProcessState.String())
		}
		if waitErr != nil {
			t = append(t, waitErr.Error())
		}
		if len(t) > 0 {
			log.Warn("v2ray-core: %v", strings.Join(t, ": "))
		}
		if !exitedBeforeReady && postUnexpectedStop != nil {
			postUnexpectedStop(process)
		}
	}()
	// ports to check
	portList := []string{strconv.Itoa(tmpl.ApiPort)}
	log.Trace("portList for connectivity test: %+v", portList)
	startTime := time.Now()
	startTimeOut := time.Duration(conf.GetEnvironmentConfig().CoreStartupTimeout) * time.Second
	for i := 0; i < len(portList); {
		conn, dialErr := net.Dial("tcp", net.JoinHostPort("127.0.0.1", portList[i]))
		if dialErr == nil {
			_ = conn.Close()
			i++
			continue
		}
		if startupState.Load() == startupStateExitedEarly {
			if log.Log.GetLevel() > log.ParseLevel("info") {
				log.Error("some critical information may lost due to your log level")
			}
			return nil, fmt.Errorf("unexpected exiting: check the log for more information")
		}
		if time.Since(startTime) > startTimeOut {
			log.Info("Attempting to terminate timed-out process with SIGTERM")
			if cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
			return nil, fmt.Errorf("timeout: check the log for more information")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !startupState.CompareAndSwap(startupStateStarting, startupStateRunning) {
		return nil, fmt.Errorf("unexpected exiting: check the log for more information")
	}
	log.Trace("Cost of waiting for v2ray-core: %v", time.Since(startTime).String())
	return process, nil
}

const maxLogInfoPendingBytes = 64 * 1024

type logInfoWriter struct {
	mu      sync.Mutex
	pending string
}

func (w *logInfoWriter) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	data := w.pending + string(p)
	var lines []string
	for {
		idx := strings.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		lines = append(lines, strings.TrimSuffix(data[:idx], "\r"))
		data = data[idx+1:]
	}
	if len(data) > maxLogInfoPendingBytes {
		lines = append(lines, strings.TrimSuffix(data, "\r"))
		data = ""
	}
	w.pending = data
	w.mu.Unlock()

	for _, line := range lines {
		w.emitLine(line)
	}
	return len(p), nil
}

func (w *logInfoWriter) Flush() {
	if w == nil {
		return
	}
	w.mu.Lock()
	rem := strings.TrimSuffix(w.pending, "\r")
	w.pending = ""
	w.mu.Unlock()
	if rem != "" {
		w.emitLine(rem)
	}
}

func (w *logInfoWriter) emitLine(line string) {
	// remove timestamp
	fields := strings.SplitN(line, " ", 3)
	if len(fields) >= 3 {
		if _, err := time.Parse("2006/01/02 15:04:05", fields[0]+" "+fields[1]); err == nil {
			recordConnectionLine(fields[2])
			log.Info("%v", fields[2])
			return
		}
	}
	recordConnectionLine(line)
	log.Info("%v", line)
}

var logWriter = &logInfoWriter{}

func (p *Process) Close() error {
	if p == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.expectedStop.Store(true)
	// 停止 xray 进程（v2raya-core 内部会同时关闭 DNS 模块）
	// 进程回收统一由唯一的 reaper goroutine (cmd.Wait) 完成，绝不在此重复 Wait。
	if p.procCancel != nil {
		cancel := p.procCancel
		p.procCancel = nil
		cancel()
	} else if p.proc != nil {
		_ = p.proc.Kill()
	}
	if p.template != nil {
		if err := p.template.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (p *Process) WaitUntilExit(ctx context.Context) error {
	if p == nil || p.done == nil {
		return nil
	}
	if ctx == nil {
		<-p.done
		return nil
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runWithLogCmd(ctx context.Context, name string, argv []string, dir string, env []string) (*exec.Cmd, *logInfoWriter, error) {
	lw := &logInfoWriter{}
	cmd := exec.CommandContext(ctx, name)
	cmd.WaitDelay = 3 * time.Second
	cmd.Args = argv
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = lw
	cmd.Stderr = lw
	err := cmd.Start()
	if err != nil {
		return nil, nil, err
	}
	return cmd, lw, nil
}

func RunWithLog(ctx context.Context, name string, argv []string, dir string, env []string) (*os.Process, error) {
	cmd, lw, err := runWithLogCmd(ctx, name, argv, dir, env)
	if err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait(); lw.Flush() }()
	return cmd.Process, nil
}

func startCoreProcessCmd(ctx context.Context) (*exec.Cmd, *logInfoWriter, error) {
	v2rayBinPath, err := where.GetV2rayBinPath()
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Dir(v2rayBinPath)
	var arguments = []string{
		v2rayBinPath,
		"run",
		"--config=" + asset.GetV2rayConfigPath(),
	}
	if confdir := asset.GetV2rayConfigDirPath(); confdir != "" {
		arguments = append(arguments, "--confdir="+confdir)
	}

	// Get asset directory
	assetDir := asset.GetV2rayLocationAssetOverride()
	log.Info("Asset directory for %s: %v", "v2raya_core", assetDir)

	// Prepare environment variables, filtering out duplicates
	env := make([]string, 0, len(os.Environ())+4)
	for _, e := range os.Environ() {
		// Skip existing V2RAY_LOCATION_ASSET, XRAY_LOCATION_ASSET, V2RAY_CONF_GEOLOADER
		if strings.HasPrefix(e, "V2RAY_LOCATION_ASSET=") ||
			strings.HasPrefix(e, "XRAY_LOCATION_ASSET=") ||
			strings.HasPrefix(e, "V2RAY_CONF_GEOLOADER=") {
			continue
		}
		env = append(env, e)
	}

	// Add asset directory to environment based on core type.
	// v2raya_core is based on xray-core and uses XRAY_LOCATION_ASSET.
	env = append(env, "XRAY_LOCATION_ASSET="+assetDir)

	// Check memory and set geoloader mode
	memstat, err := mem.VirtualMemory()
	if err != nil {
		log.Warn("cannot get memory info: %v", err)
	} else {
		if memMiB := memstat.Available / 1024 / 1024; memMiB < 2048 {
			env = append(env, "V2RAY_CONF_GEOLOADER=memconservative")
			log.Info("low memory: %vMiB, set V2RAY_CONF_GEOLOADER=memconservative", memMiB)
		}
	}

	log.Debug(strings.Join(arguments, " "))
	cmd, lw, err := runWithLogCmd(ctx, v2rayBinPath, arguments, dir, env)
	if err != nil {
		return nil, nil, err
	}
	return cmd, lw, nil
}

func StartCoreProcess(ctx context.Context) (*os.Process, error) {
	cmd, lw, err := startCoreProcessCmd(ctx)
	if err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait(); lw.Flush() }()
	return cmd.Process, nil
}

func findAvailablePluginPorts(vms []serverObj.ServerObj) (pluginPortMap map[int]int, err error) {
	pluginPortMap = make(map[int]int)
	for i, v := range vms {
		if !v.NeedPluginPort() {
			continue
		}
		//find a port that not be occupied
		var port int
		for {
			// Find a port >= 30000
			r := 30000 + common.RandInt(35535)
			l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%v", r))
			if err == nil {
				defer l.Close()
				port = l.Addr().(*net.TCPAddr).Port
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		pluginPortMap[i] = port
	}
	return pluginPortMap, nil
}

func getConnectedServerObjs() ([]serverObj.ServerObj, []serverInfo, error) {
	css := configure.GetConnectedServers()
	if css.Len() == 0 { //no connected server. stop v2ray-core.
		return nil, nil, nil
	}
	serverInfos := make([]serverInfo, 0, css.Len())
	serverObjs := make([]serverObj.ServerObj, 0, css.Len())
	for _, cs := range css.Get() {
		sr, err := cs.LocateServerRaw()
		if err != nil {
			return nil, nil, err
		}
		serverInfos = append(serverInfos, serverInfo{
			Info:         sr.ServerObj,
			OutboundName: cs.Outbound,
		})
		if cs.TYPE == configure.SubscriptionServerType {
			if sub := configure.GetSubscription(cs.Sub); sub != nil {
				serverInfos[len(serverInfos)-1].NodeDNS = sub.Extra.EffectiveNodeDNS()
			}
		}
		serverObjs = append(serverObjs, sr.ServerObj)
	}
	return serverObjs, serverInfos, nil
}

func NewTemplateFromConnectedServers(setting *configure.Setting) (tmpl *Template, err error) {
	//read the database and convert to the v2ray-core template
	serverObjs, serverInfos, err := getConnectedServerObjs()
	if err != nil {
		return nil, err
	}
	if len(serverObjs) == 0 {
		return nil, NoConnectedServerErr
	}
	var pluginPorts map[int]int
	if pluginPorts, err = findAvailablePluginPorts(serverObjs); err != nil {
		return nil, err
	}
	for i := range serverInfos {
		if port, ok := pluginPorts[i]; ok {
			serverInfos[i].PluginPort = port
		}
	}
	tmpl, err = NewTemplate(serverInfos, setting)
	if err != nil {
		return nil, err
	}
	return tmpl, nil
}

func UpdateV2RayConfig() (err error) {
	tmpl, err := NewTemplateFromConnectedServers(nil)
	if err != nil {
		if errors.Is(err, NoConnectedServerErr) {
			// An empty selection revokes the last active route. The API
			// must not report success while its old SOCKS listener is live.
			stopped := ProcessManager.stopCurrent(true)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := stopped.WaitUntilExit(ctx); err != nil {
				return fmt.Errorf("waiting for the previous core to exit: %w", err)
			}
			return nil
		}
		return err
	}
	err = ProcessManager.Start(tmpl)
	if err != nil {
		return err
	}
	return
}

// rollback 回滚已启动的组件。
// 注意：透明代理规则回滚由 processManager.afterStart 的 defer 处理。
func (p *Process) rollback(stage int) {
	if stage == 1 {
		// xray 已启动 → 回滚
		p.mutex.Lock()
		p.expectedStop.Store(true)
		cancel := p.procCancel
		p.procCancel = nil
		p.mutex.Unlock()
		if cancel != nil {
			cancel()
		}
		<-p.done
	}
}
