package main

// MCP 자동 등록 — AI 프로그램(Claude Desktop 등) 설정에 이 exe 를 MCP 서버로 적어 준다.
//
// 왜 필요한가: 배포 파일 이름에는 버전·커밋 해시가 붙어(예: 교실_퀴즈-0.0.0-abc1234.exe)
// 새로 받을 때마다 경로가 달라진다. 교사가 그때마다 설정 파일의 경로를 고쳐 넣는 것은
// 현실적이지 않다. 그래서 exe 가 제 경로를 스스로 적는다.
//
//   · 교사 화면의 [AI 연결] 버튼 또는 `classroom-quiz.exe mcp-install` → 새로 등록(명시적).
//   · 그 다음부터는 앱을 켤 때마다 이미 등록된 항목의 경로만 조용히 최신으로 맞춘다.
//     (등록한 적이 없으면 아무것도 만들지 않는다 — 남의 설정 파일을 함부로 건드리지 않는다.)

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// 설정 파일 안에서 우리 서버를 가리키는 이름. 파일 이름이 바뀌어도 이 키는 고정이다.
const mcpServerKey = "classroom-quiz"

// mcpClientConfigPath 는 Claude Desktop 설정 파일의 표준 위치.
// 다른 AI 프로그램(Cursor·VS Code 등)도 같은 mcpServers 형식이라, 경로만 인자로 주면 된다.
func mcpClientConfigPath() string {
	switch runtime.GOOS {
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			return filepath.Join(ad, "Claude", "claude_desktop_config.json")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		}
	default:
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
		}
	}
	return ""
}

// selfPath 는 지금 실행 중인 exe 의 절대 경로.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Clean(exe), nil
}

// installMCP 은 설정 파일에 이 exe 를 MCP 서버로 등록한다. 다른 서버 설정과 우리 항목의
// 다른 필드(env 등)는 그대로 두고 실행 경로만 맞춘다.
//
// create=true  : 항목이 없으면 만든다(교사가 [AI 연결]을 눌렀을 때).
// create=false : 이미 있는 항목의 경로만 고친다. 없으면 아무것도 하지 않는다(앱 시작 시 자동 갱신).
//
// 반환: 파일을 고쳤는지, 사람에게 보여줄 안내 문구.
func installMCP(cfgPath string, create bool) (bool, string, error) {
	if cfgPath == "" {
		return false, "", errors.New("AI 프로그램 설정 파일 위치를 찾지 못했습니다.")
	}
	exe, err := selfPath()
	if err != nil {
		return false, "", fmt.Errorf("실행 파일 경로를 알 수 없습니다: %w", err)
	}

	raw, readErr := os.ReadFile(cfgPath)
	switch {
	case errors.Is(readErr, os.ErrNotExist):
		if !create {
			return false, "", nil // 설정 파일이 없다 = 아직 AI 연결을 쓴 적이 없다.
		}
		raw = nil
	case readErr != nil:
		return false, "", fmt.Errorf("설정 파일을 읽지 못했습니다: %w", readErr)
	}

	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			// 사람이 손으로 고치다 깨졌을 수 있다. 덮어쓰면 다른 연결까지 날아가므로 멈춘다.
			return false, "", fmt.Errorf("설정 파일 형식이 올바르지 않아 건드리지 않았습니다(%s): %w", cfgPath, err)
		}
	}

	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		if !create && len(cfg) > 0 {
			return false, "", nil
		}
		servers = map[string]any{}
	}

	entry, exists := servers[mcpServerKey].(map[string]any)
	if !exists {
		if !create {
			return false, "", nil // 등록한 적이 없으면 만들지 않는다.
		}
		entry = map[string]any{}
	}
	if cur, _ := entry["command"].(string); cur == exe && exists {
		return false, "이미 연결돼 있습니다(경로도 최신입니다).", nil
	}

	entry["command"] = exe
	entry["args"] = []any{"mcp"}
	servers[mcpServerKey] = entry
	cfg["mcpServers"] = servers

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, "", err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		return false, "", err
	}
	// 남의 설정 파일이다. 고치기 전에 사본을 남기고, 원자적으로(temp→rename) 바꾼다.
	if len(raw) > 0 {
		_ = os.WriteFile(cfgPath+".bak", raw, 0644)
	}
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return false, "", err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		return false, "", err
	}

	if exists {
		return true, "AI 연결 경로를 새 실행 파일로 맞췄습니다.", nil
	}
	return true, "AI 연결을 등록했습니다. AI 프로그램을 완전히 껐다가 다시 켜면 '교실 퀴즈' 도구가 붙습니다.", nil
}

// refreshMCPRegistration 은 앱을 켤 때 등록해 둔 경로를 조용히 최신으로 맞춘다.
// (새 버전을 받아 파일 이름이 바뀌어도 AI 연결이 끊기지 않게 — 등록이 없으면 아무 일도 안 한다.)
func refreshMCPRegistration() {
	changed, msg, err := installMCP(mcpClientConfigPath(), false)
	if err != nil {
		log.Printf("AI 연결 경로 확인 실패: %v", err)
		return
	}
	if changed {
		log.Printf("AI 연결(MCP) %s", msg)
	}
}

// runMCPInstall 은 `classroom-quiz.exe mcp-install` 진입점. 인자로 설정 파일 경로를 주면
// 그 파일에 등록한다(Claude Desktop 외 다른 AI 프로그램용).
func runMCPInstall(args []string) {
	cfgPath := mcpClientConfigPath()
	if len(args) > 0 && args[0] != "" {
		cfgPath = args[0]
	}
	_, msg, err := installMCP(cfgPath, true)
	if err != nil {
		fmt.Println("실패:", err)
		log.Printf("MCP 등록 실패: %v", err)
		os.Exit(1)
	}
	exe, _ := selfPath()
	fmt.Printf("%s\n  설정 파일: %s\n  실행 파일: %s\n", msg, cfgPath, exe)
	log.Printf("MCP 등록: %s (%s)", cfgPath, exe)
}
