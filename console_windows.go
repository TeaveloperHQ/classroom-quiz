//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// hideOwnConsole 은 MCP 모드에서 이 프로세스의 콘솔 창을 숨긴다.
//
// AI 프로그램(GUI)이 이 exe 를 자식으로 띄우면 콘솔 창이 새로 열릴 수 있다. 교사 눈에는
// 정체불명의 검은 창이고, 닫으면 AI 연결이 끊긴다. 창만 숨기고 표준입출력 통신은 그대로 둔다.
//
// 교사가 명령 프롬프트에서 직접 실행한 경우에는 그 창이 교사의 것이므로 숨기면 안 된다.
// 콘솔에 붙어 있는 프로세스가 우리 하나뿐일 때(= 우리 때문에 열린 창일 때)만 숨긴다.
func hideOwnConsole() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	user32 := windows.NewLazySystemDLL("user32.dll")

	var pids [8]uint32
	n, _, _ := kernel32.NewProc("GetConsoleProcessList").Call(
		uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return // 콘솔을 남과 함께 쓰는 중(명령 프롬프트에서 실행) — 남의 창을 건드리지 않는다.
	}
	hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if hwnd == 0 {
		return // 콘솔이 아예 없다(GUI 가 창 없이 띄운 경우) — 할 일 없음.
	}
	const swHide = 0
	user32.NewProc("ShowWindow").Call(hwnd, swHide)
}
