//go:build !windows

package main

import (
	"fmt"
	"os"
)

// 윈도우 밖은 대상이 아니다 — 부모 번호만 적고, 감시는 하지 않는다.
func parentChain() string { return fmt.Sprintf("pid %d", os.Getppid()) }
func watchParent()        {}
