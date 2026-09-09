package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 배포 파일 이름은 빌드마다 달라진다(버전·해시). 그래서 exe 가 제 경로를 스스로 적는데,
// 그 대상이 남의 설정 파일이라 "다른 설정을 건드리지 않는다"가 핵심이다.

func readCfg(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("설정 파일 읽기 실패: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("설정 파일 JSON 파싱 실패: %v", err)
	}
	return m
}

func ourEntry(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers 가 없다: %v", cfg)
	}
	e, ok := servers[mcpServerKey].(map[string]any)
	if !ok {
		t.Fatalf("%s 항목이 없다: %v", mcpServerKey, servers)
	}
	return e
}

// 설정 파일이 없으면 만들어 준다([AI 연결] 버튼).
func TestInstallCreatesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Claude", "claude_desktop_config.json")
	changed, msg, err := installMCP(path, true)
	if err != nil {
		t.Fatalf("등록 실패: %v", err)
	}
	if !changed || msg == "" {
		t.Fatalf("등록됐다고 알려야 한다 (changed=%v msg=%q)", changed, msg)
	}
	e := ourEntry(t, readCfg(t, path))
	exe, _ := selfPath()
	if e["command"] != exe {
		t.Errorf("command = %v, 지금 실행 파일(%s)이어야 한다", e["command"], exe)
	}
	args, _ := e["args"].([]any)
	if len(args) != 1 || args[0] != "mcp" {
		t.Errorf("args = %v, [mcp] 여야 한다", e["args"])
	}
}

// 다른 MCP 서버 설정과 우리 항목의 다른 필드는 그대로 둬야 한다.
func TestInstallPreservesOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	orig := `{
  "theme": "dark",
  "mcpServers": {
    "other-app": { "command": "C:\\other\\app.exe", "args": ["serve"] },
    "classroom-quiz": { "type": "stdio", "command": "C:\\옛경로\\교실_퀴즈-0.0.0-old.exe", "args": ["mcp"], "env": { "K": "V" } }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0644); err != nil {
		t.Fatal(err)
	}

	changed, _, err := installMCP(path, false) // 앱 시작 시 자동 갱신
	if err != nil {
		t.Fatalf("갱신 실패: %v", err)
	}
	if !changed {
		t.Fatal("옛 경로가 남아 있으므로 고쳐야 한다")
	}

	cfg := readCfg(t, path)
	if cfg["theme"] != "dark" {
		t.Errorf("관계없는 설정(theme)이 사라졌다: %v", cfg["theme"])
	}
	servers := cfg["mcpServers"].(map[string]any)
	if _, ok := servers["other-app"]; !ok {
		t.Error("다른 MCP 서버 설정이 사라졌다")
	}
	e := ourEntry(t, cfg)
	exe, _ := selfPath()
	if e["command"] != exe {
		t.Errorf("command = %v, 새 실행 파일(%s)로 바뀌어야 한다", e["command"], exe)
	}
	if env, _ := e["env"].(map[string]any); env == nil || env["K"] != "V" {
		t.Errorf("우리 항목의 다른 필드(env)가 사라졌다: %v", e["env"])
	}
	// Claude Code 는 항목에 type:"stdio" 를 넣는다 — 우리가 경로만 고치고 지우면 안 된다.
	if e["type"] != "stdio" {
		t.Errorf("우리 항목의 type 필드가 사라졌다: %v", e["type"])
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("고치기 전 사본(.bak)을 남겨야 한다: %v", err)
	}

	// 두 번째 호출은 이미 최신이라 파일을 건드리지 않아야 한다.
	if changed, _, err := installMCP(path, false); err != nil || changed {
		t.Errorf("최신 상태에서 또 고쳤다 (changed=%v err=%v)", changed, err)
	}
}

// 등록한 적이 없으면 앱 시작 시 아무것도 만들지 않는다(남의 설정에 멋대로 끼어들지 않는다).
func TestRefreshDoesNotCreate(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "cfg.json")
	if changed, _, err := installMCP(missing, false); err != nil || changed {
		t.Errorf("파일이 없으면 만들지 말아야 한다 (changed=%v err=%v)", changed, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("설정 파일이 생겼다")
	}

	other := filepath.Join(dir, "other.json")
	orig := `{"mcpServers":{"other-app":{"command":"x"}}}`
	if err := os.WriteFile(other, []byte(orig), 0644); err != nil {
		t.Fatal(err)
	}
	if changed, _, err := installMCP(other, false); err != nil || changed {
		t.Errorf("우리 항목이 없으면 끼워 넣지 말아야 한다 (changed=%v err=%v)", changed, err)
	}
	b, _ := os.ReadFile(other)
	if string(b) != orig {
		t.Errorf("파일이 바뀌었다:\n%s", b)
	}
}

// 교사가 어떤 AI 프로그램을 쓸지 모르므로, 등록해 준 설정 파일 경로를 기억해 두고
// 그 파일들도 자동 갱신 대상으로 삼는다(Claude Desktop 전용이 아니다).
func TestRememberedClientsAreRefreshed(t *testing.T) {
	t.Setenv("CLASSROOM_QUIZ_HOME", t.TempDir()) // mcp-clients.json 을 임시 폴더에
	dir := t.TempDir()
	cursor := filepath.Join(dir, "cursor-mcp.json")
	if err := os.WriteFile(cursor, []byte(`{"mcpServers":{"classroom-quiz":{"command":"C:\\old\\quiz.exe","args":["mcp"]}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	rememberClient(cursor)
	if got := rememberedClients(); len(got) != 1 || got[0] != cursor {
		t.Fatalf("기억한 목록 = %v, [%s] 여야 한다", got, cursor)
	}
	rememberClient(cursor) // 같은 경로는 한 번만
	if got := rememberedClients(); len(got) != 1 {
		t.Errorf("같은 경로가 중복 기록됐다: %v", got)
	}

	refreshMCPRegistration() // 앱을 켤 때 하는 일

	exe, _ := selfPath()
	if e := ourEntry(t, readCfg(t, cursor)); e["command"] != exe {
		t.Errorf("기억해 둔 설정 파일이 갱신되지 않았다: %v", e["command"])
	}
}

// 손으로 고치다 깨진 설정 파일은 덮어쓰지 않는다(다른 연결까지 날아간다).
func TestInstallRefusesBrokenConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	broken := `{ "mcpServers": { "other-app": ... }`
	if err := os.WriteFile(path, []byte(broken), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := installMCP(path, true); err == nil {
		t.Fatal("깨진 설정 파일은 오류로 알려야 한다")
	}
	b, _ := os.ReadFile(path)
	if string(b) != broken {
		t.Errorf("깨진 파일을 건드렸다:\n%s", b)
	}
}
