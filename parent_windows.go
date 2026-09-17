//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AI 프로그램을 닫아도 `mcp` 프로세스가 안 끝나고 남는 문제(docs/KNOWN_ISSUES.md 1번).
//
// 원래는 부모가 끝나면 stdin 파이프가 닫혀 EOF 로 빠져나온다. 그런데 부모가 파이프의
// 쓰기 끝을 상속 가능하게 둔 채 다른 자식을 띄우면 그 자식이 쓰기 핸들 사본을 쥐게 되고,
// 부모가 죽어도 EOF 가 오지 않는다(시뮬레이션으로 재현함). 남은 프로세스는 exe 파일을
// 잠가, 교사가 새 버전으로 덮어쓰거나 옛 파일을 지우지 못하게 만든다.
//
// 그래서 파이프에 기대지 않고 부모 프로세스를 직접 기다린다. `mcp` 모드는 게임 허브를
// 띄우지 않으므로(main.go 에서 그 전에 return) 언제 끝내도 학생 연결에는 영향이 없다.

// 셸 래퍼는 자식이 끝날 때까지 함께 살아 있으므로 기다려 봐야 소용없다 — 그 위를 본다.
var shellWrappers = map[string]bool{
	"cmd.exe": true, "powershell.exe": true, "pwsh.exe": true, "conhost.exe": true,
}

type procInfo struct {
	name   string
	parent uint32
}

func snapshotProcesses() map[uint32]procInfo {
	procs := map[uint32]procInfo{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return procs
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		procs[e.ProcessID] = procInfo{name: windows.UTF16ToString(e.ExeFile[:]), parent: e.ParentProcessID}
	}
	return procs
}

// 시작할 때 부모 사슬 로그와 부모 감시가 같은 목록을 쓰도록 스냅샷은 한 번만 찍는다
// (프로세스 수백 개를 훑는 데 수십 ms 가 든다 — 측정값). 이름·부모 번호만 쓰고, 살아 있는지와
// PID 재사용 여부는 감시할 때 OpenProcess·생성 시각으로 따로 확인하므로 캐시해도 안전하다.
var (
	procSnapOnce sync.Once
	procSnap     map[uint32]procInfo
)

func processes() map[uint32]procInfo {
	procSnapOnce.Do(func() { procSnap = snapshotProcesses() })
	return procSnap
}

// myParent 는 스냅샷에서 우리 부모 번호를 읽는다. os.Getppid() 는 윈도우에서 호출할
// 때마다 스냅샷을 새로 찍어 느리므로 쓰지 않는다.
func myParent(procs map[uint32]procInfo) uint32 {
	return procs[uint32(os.Getpid())].parent
}

// createdAt 은 프로세스 생성 시각(100ns 단위). 열지 못하면 0.
func createdAt(h windows.Handle) int64 {
	var c, x, k, u windows.Filetime
	if windows.GetProcessTimes(h, &c, &x, &k, &u) != nil {
		return 0
	}
	return c.Nanoseconds()
}

// parentChain 은 부모 사슬을 사람이 읽게 적는다(시작 로그용).
// 예: "claude.exe(6016) ← Code.exe(4410) ← explorer.exe(3120)"
func parentChain() string {
	procs := processes()
	var parts []string
	pid := myParent(procs)
	for i := 0; i < 4 && pid != 0; i++ {
		p, ok := procs[pid]
		if !ok {
			parts = append(parts, fmt.Sprintf("(끝난 프로세스)(%d)", pid))
			break
		}
		parts = append(parts, fmt.Sprintf("%s(%d)", p.name, pid))
		pid = p.parent
	}
	return strings.Join(parts, " ← ")
}

// watchParent 는 우리를 띄운 프로그램(셸 래퍼는 건너뜀)이 끝나면 이 프로세스도 끝낸다.
func watchParent() {
	me := createdAt(windows.CurrentProcess())
	procs := processes()
	pid := myParent(procs)

	for i := 0; i < 4 && pid != 0; i++ {
		info, ok := procs[pid]
		if !ok {
			log.Printf("부모 감시 안 함: 부모(%d)가 이미 없다", pid)
			return
		}
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			log.Printf("부모 감시 안 함: %s(%d) 를 열 수 없다: %v", info.name, pid, err)
			return
		}
		// PID 재사용 방지 — 진짜 조상이면 우리보다 먼저 생겼어야 한다.
		if born := createdAt(h); born == 0 || me == 0 || born > me {
			windows.CloseHandle(h)
			log.Printf("부모 감시 안 함: %s(%d) 는 우리보다 늦게 생긴 프로세스(PID 재사용)", info.name, pid)
			return
		}
		if shellWrappers[strings.ToLower(info.name)] {
			windows.CloseHandle(h)
			me = createdAt(windows.CurrentProcess()) // 조상 비교는 계속 "우리보다 앞" 기준
			pid = info.parent
			continue
		}
		go func(h windows.Handle, name string, pid uint32) {
			windows.WaitForSingleObject(h, windows.INFINITE)
			log.Printf("MCP 서버 종료(부모 %s(%d) 가 끝남)", name, pid)
			os.Exit(0)
		}(h, info.name, pid)
		return
	}
}
