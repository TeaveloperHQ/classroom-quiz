//go:build !windows

package main

// 윈도우 밖에서는 콘솔 창 문제가 없다(교사 배포 대상은 윈도우).
func hideOwnConsole() {}
