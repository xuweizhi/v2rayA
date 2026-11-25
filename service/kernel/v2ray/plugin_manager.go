package v2ray

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

type pluginManagerNode struct {
	link string
	port int
}

func (t *Template) startPluginManagers(ctx context.Context) error {
	for _, node := range t.pluginManagers {
		env := conf.GetEnvironmentConfig()
		processCtx, cancel := context.WithCancel(ctx)
		cmd := exec.CommandContext(processCtx, env.PluginManager, "--stage=run", "--link="+node.link,
			"--port="+strconv.Itoa(node.port), "--v2raya-confdir="+env.Config)
		if err := cmd.Start(); err != nil {
			cancel()
			return fmt.Errorf("start plugin manager on port %d: %w", node.port, err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := cmd.Wait(); err != nil && processCtx.Err() == nil {
				log.Warn("plugin manager on port %d exited: %v", node.port, err)
			}
		}()
		t.ApiCloses = append(t.ApiCloses, func() {
			cancel()
			<-done
		})
	}
	return nil
}
