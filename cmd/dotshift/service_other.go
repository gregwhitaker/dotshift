//go:build !unix

package main

import (
	"errors"
	"os/exec"
)

func lockService(string) (func(), error) {
	return nil, errors.New("the local dotshift service currently requires macOS or Linux")
}
func detachService(*exec.Cmd) {}
