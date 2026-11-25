package common

import (
	"bytes"
	"crypto/rand"
	"encoding/gob"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)

func RandInt(max int) int {
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(nBig.Int64())
}

var (
	NotSameTypeErr    = fmt.Errorf("cannot fill empty: the two value have different type")
	NeedPassInPointer = fmt.Errorf("the structure passed in should be a pointer")
)

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func Min(a, b int) int {
	if a > b {
		return b
	}
	return a
}

func Abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func BoolToInt(a bool) int {
	if a {
		return 1
	}
	return 0
}

func BoolToString(a bool) string {
	if a {
		return "true"
	}
	return "false"
}

func VersionMustGreaterEqual(v1, v2 string) (is bool) {
	is, _ = VersionGreaterEqual(v1, v2)
	return
}

func Deduplicate(list []string) []string {
	res := make([]string, 0, len(list))
	m := make(map[string]struct{})
	for _, v := range list {
		if _, ok := m[v]; ok {
			continue
		}
		m[v] = struct{}{}
		res = append(res, v)
	}
	return res
}

func PrefixListSatisfyString(prefixList []string, str string) int {
	for i, v := range prefixList {
		if strings.HasPrefix(str, v) {
			return i
		}
	}
	return -1
}

func parseSemVer(v string) (core []int, pre []string, err error) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	// Strip build metadata (+...)
	if idx := strings.IndexByte(v, '+'); idx >= 0 {
		v = v[:idx]
	}
	coreStr := v
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		coreStr = v[:idx]
		preStr := v[idx+1:]
		if preStr != "" {
			pre = strings.Split(preStr, ".")
		}
	}
	if coreStr == "" {
		return nil, nil, fmt.Errorf("empty version")
	}
	parts := strings.Split(coreStr, ".")
	core = make([]int, len(parts))
	for i, part := range parts {
		n, convErr := strconv.Atoi(part)
		if convErr != nil || n < 0 {
			return nil, nil, fmt.Errorf("invalid version segment %q in %q", part, v)
		}
		core[i] = n
	}
	return core, pre, nil
}

/* return if v1 is after v2 */
func VersionGreaterEqual(v1, v2 string) (is bool, err error) {
	var HighPriority = []string{"debug", "unstable"}
	if PrefixListSatisfyString(HighPriority, v1) != -1 {
		return true, nil
	}
	if PrefixListSatisfyString(HighPriority, v2) != -1 {
		return false, nil
	}
	c1, p1, err := parseSemVer(v1)
	if err != nil {
		return false, err
	}
	c2, p2, err := parseSemVer(v2)
	if err != nil {
		return false, err
	}
	maxLen := Max(len(c1), len(c2))
	for i := 0; i < maxLen; i++ {
		n1, n2 := 0, 0
		if i < len(c1) {
			n1 = c1[i]
		}
		if i < len(c2) {
			n2 = c2[i]
		}
		if n1 < n2 {
			return false, nil
		} else if n1 > n2 {
			return true, nil
		}
	}
	// Core numbers are equal; release (no prerelease) > any prerelease.
	if len(p1) == 0 && len(p2) == 0 {
		return true, nil
	}
	if len(p1) == 0 {
		return true, nil
	}
	if len(p2) == 0 {
		return false, nil
	}
	minPre := Min(len(p1), len(p2))
	for i := 0; i < minPre; i++ {
		if p1[i] == p2[i] {
			continue
		}
		num1, err1 := strconv.Atoi(p1[i])
		num2, err2 := strconv.Atoi(p2[i])
		switch {
		case err1 == nil && err2 == nil:
			return num1 > num2, nil
		case err1 == nil:
			// SemVer: numeric identifiers always have lower precedence than non-numeric.
			return false, nil
		case err2 == nil:
			return true, nil
		default:
			return p1[i] > p2[i], nil
		}
	}
	return len(p1) >= len(p2), nil
}

// IsDocker return true only if the environment SHOULD be docker
func IsDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// UrlEncoded encodes a string like Javascript's encodeURIComponent()
func UrlEncoded(str string) string {
	u, err := url.Parse(str)
	if err != nil {
		return str
	}
	return u.String()
}

func TrimLineContains(parent, sub string) string {
	lines := strings.Split(parent, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.Contains(line, sub) {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// FillEmpty fill the empty field of the struct with default value given
func FillEmpty(toFill interface{}, defaultVal interface{}) error {
	if toFill == nil || defaultVal == nil {
		return NeedPassInPointer
	}
	ta := reflect.TypeOf(toFill)
	if ta.Kind() != reflect.Ptr {
		return NeedPassInPointer
	}
	tb := reflect.TypeOf(defaultVal)
	va := reflect.ValueOf(toFill)
	vb := reflect.ValueOf(defaultVal)
	for ta.Kind() == reflect.Ptr {
		if !va.IsValid() || va.IsNil() {
			return NeedPassInPointer
		}
		ta = ta.Elem()
		va = va.Elem()
	}
	for tb.Kind() == reflect.Ptr {
		if !vb.IsValid() || vb.IsNil() {
			return NotSameTypeErr
		}
		tb = tb.Elem()
		vb = vb.Elem()
	}
	if ta != tb || !va.IsValid() || !vb.IsValid() || ta.Kind() != reflect.Struct {
		return NotSameTypeErr
	}
	for i := 0; i < va.NumField(); i++ {
		v := va.Field(i)
		if !v.IsValid() || !v.CanSet() {
			continue
		}
		if v.Kind() == reflect.Bool {
			continue
		}
		if v.IsZero() {
			defField := vb.Field(i)
			if defField.IsValid() {
				v.Set(defField)
			}
		}
	}
	return nil
}

// IsOpenWrt return true only if the operating system SHOULD be openwrt
func IsOpenWrt() bool {
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/etc/openwrt_release"); err == nil {
			return true
		}
	}
	return false
}

func SliceSub(slice []string, toSub []string) []string {
	var res = make([]string, 0, len(slice))
	var m = make(map[string]struct{})
	for _, s := range toSub {
		m[s] = struct{}{}
	}
	for _, s := range slice {
		if _, ok := m[s]; !ok {
			res = append(res, s)
		}
	}
	return res
}

func SliceHas(slice []string, set []string) []string {
	var res = make([]string, 0, len(slice))
	var m = make(map[string]struct{})
	for _, s := range set {
		m[s] = struct{}{}
	}
	for _, s := range slice {
		if _, ok := m[s]; ok {
			res = append(res, s)
		}
	}
	return res
}

func SliceToSet(slice []string) map[string]struct{} {
	var m = make(map[string]struct{})
	for _, s := range slice {
		m[s] = struct{}{}
	}
	return m
}

func BytesCopy(b []byte) []byte {
	var a = make([]byte, len(b))
	copy(a, b)
	return a
}

func ToBytes(val interface{}) (b []byte, err error) {
	buf := new(bytes.Buffer)
	if err = gob.NewEncoder(buf).Encode(val); err != nil {
		return nil, err
	}
	return BytesCopy(buf.Bytes()), nil
}

func HomeExpand(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}

	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return usr.HomeDir, nil
	}
	return filepath.Join(usr.HomeDir, path[2:]), nil
}

func HasAnyPrefix(s string, prefix []string) bool {
	for _, p := range prefix {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
