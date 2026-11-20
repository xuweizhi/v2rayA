//go:build !linux

package v2ray

import "os"

const HijackFlag = "# v2rayA DNS hijack"

type ResolvHijacker struct{}

func NewResolvHijacker() *ResolvHijacker    { return nil }
func (*ResolvHijacker) Close() error        { return nil }
func (*ResolvHijacker) HijackResolv() error { return nil }

func readSystemResolvConf() ([]byte, error) { return os.ReadFile("/etc/resolv.conf") }

func RecoverResolvConf() error   { return nil }
func resetResolvHijacker() error { return nil }
func removeResolvHijacker()      {}
