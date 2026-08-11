//go:build !linux

package main

import "syscall"

func vaultwardenSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
