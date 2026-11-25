package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/conf"
)

var (
	updateStateMu  sync.RWMutex
	checkUpdateURL = "https://raw.githubusercontent.com/v2rayA/v2raya-apt/master/dists/v2raya/main/binary-amd64/Packages"
)

func SetRemoteUpdateInfo(foundNew bool, remoteVersion string) {
	updateStateMu.Lock()
	conf.FoundNew = foundNew
	conf.RemoteVersion = remoteVersion
	updateStateMu.Unlock()
}

func GetRemoteUpdateInfo() (foundNew bool, remoteVersion string) {
	updateStateMu.RLock()
	foundNew = conf.FoundNew
	remoteVersion = conf.RemoteVersion
	updateStateMu.RUnlock()
	return
}

func CheckUpdate() (foundNew bool, remoteVersion string, err error) {
	resp, err := http.Get(checkUpdateURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("failed to get latest version from Package file: HTTP %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	if err != nil && err != io.EOF {
		return false, "", err
	}
	s := buf.String()
	l := strings.Index(s, "Package: v2raya")
	if l < 0 {
		return false, "", fmt.Errorf("failed to get latest version from Package file: 1")
	}
	s = s[l:]
	prefix := "Version: "
	l = strings.Index(s, prefix)
	if l < 0 {
		return false, "", fmt.Errorf("failed to get latest version from Package file: 2")
	}
	s = s[l+len(prefix):]
	r := strings.Index(s, "\n")
	if r < 0 { // Go to end if no newline
		r = len(s)
	}
	s = strings.TrimSpace(s[:r])
	// Remote version obtained
	ge, err := common.VersionGreaterEqual(conf.Version, s)
	return !ge, s, err
}
