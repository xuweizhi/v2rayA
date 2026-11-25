//go:build windows || (js && wasm)
// +build windows js,wasm

package ipforward

func IsIpForwardOn() bool {
	return true
}

func WriteIpForward(on bool) (err error) {
	return nil
}
