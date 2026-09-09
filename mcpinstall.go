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
	"strings"
)

// 설정 파일 안에서 우리 서버를 가리키는 이름. 파일 이름이 바뀌어도 이 키는 고정이다.
const mcpServerKey = "classroom-quiz"

// 등록해 준 설정 파일 목록을 적어 두는 곳(퀴즈·결과와 같은 데이터 폴더).
// 교사가 어떤 AI 프로그램을 쓰는지 우리는 모르므로, "등록해 준 곳"을 기억해 두고
// 그 파일들의 경로만 앱 시작 때 최신으로 맞춘다.
func mcpClientsListPath() string {
	return filepath.Join(dataDir(), "mcp-clients.json")
}

// mcpClient 는 교사 PC 에 있을 법한 AI 프로그램의 설정 파일 한 곳.
//
// 누가 내려받아 쓸지 모르므로 특정 프로그램을 가정하지 않는다. 아래 목록을 훑되
// **이미 있는 파일만** 건드리고(그 프로그램을 쓴다는 뜻), 하나도 없으면 가장 흔한
// Claude Desktop 자리에 새로 만든다. 목록에 없는 프로그램은 설정 파일 경로를 주면 된다
// (`mcp-install <경로>`) — 그 경로는 기억해 두었다가 다음부터 함께 갱신한다.
type mcpClient struct {
	name       string
	path       string
	createHere bool // 설정 파일이 하나도 없을 때 새로 만들 자리
}

func knownMCPClients() []mcpClient {
	home, _ := os.UserHomeDir()
	var desktop string
	switch runtime.GOOS {
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			desktop = filepath.Join(ad, "Claude", "claude_desktop_config.json")
		}
	case "darwin":
		if home != "" {
			desktop = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		}
	default:
		if home != "" {
			desktop = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
		}
	}

	list := []mcpClient{{name: "Claude Desktop", path: desktop, createHere: true}}
	if home != "" {
		// 같은 mcpServers 형식을 쓰는 것들 — 파일이 있을 때만 손댄다.
		list = append(list,
			mcpClient{name: "Claude Code", path: filepath.Join(home, ".claude.json")},
			mcpClient{name: "Cursor", path: filepath.Join(home, ".cursor", "mcp.json")},
		)
	}
	out := list[:0]
	for _, c := range list {
		if c.path != "" {
			out = append(out, c)
		}
	}
	return out
}

// mcpClientConfigPath 는 설정 파일을 새로 만들 자리(가장 흔한 Claude Desktop).
func mcpClientConfigPath() string {
	for _, c := range knownMCPClients() {
		if c.createHere {
			return c.path
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

// rememberClient 는 등록해 준 설정 파일 경로를 목록에 더한다(중복은 무시).
// 교사가 Claude Desktop 을 쓰든 Cursor·VS Code·Claude Code 를 쓰든, 다음부터는
// 그 파일이 자동 갱신 대상이 된다.
func rememberClient(cfgPath string) {
	list := rememberedClients()
	for _, p := range list {
		if strings.EqualFold(p, cfgPath) {
			return
		}
	}
	list = append(list, cfgPath)
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(dataDir(), 0755); err != nil {
		return
	}
	tmp := mcpClientsListPath() + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		_ = os.Rename(tmp, mcpClientsListPath())
	}
}

func rememberedClients() []string {
	b, err := os.ReadFile(mcpClientsListPath())
	if err != nil {
		return nil
	}
	var list []string
	if json.Unmarshal(b, &list) != nil {
		return nil
	}
	return list
}

// mcpResult 는 설정 파일 한 곳의 처리 결과.
type mcpResult struct {
	Name    string
	Path    string
	Changed bool
	Msg     string
	Err     error
}

// installMCPAll 은 교사 PC 에 있는 AI 프로그램 설정을 훑어 등록/갱신한다.
//
// create=true 는 교사가 [AI 연결]을 눌렀을 때 — 있는 설정 파일에는 모두 등록하고, 하나도
// 없으면 Claude Desktop 자리에 새로 만든다. create=false 는 앱을 켤 때의 자동 갱신으로,
// 이미 등록된 항목의 경로만 고친다.
func installMCPAll(create bool) []mcpResult {
	type target struct{ name, path string }
	var targets []target
	seen := map[string]bool{}
	add := func(name, path string) {
		if path == "" || seen[strings.ToLower(path)] {
			return
		}
		seen[strings.ToLower(path)] = true
		targets = append(targets, target{name, path})
	}

	anyExists := false
	for _, c := range knownMCPClients() {
		if _, err := os.Stat(c.path); err == nil {
			anyExists = true
			add(c.name, c.path)
		}
	}
	for _, p := range rememberedClients() { // 교사가 직접 알려 준 프로그램
		if _, err := os.Stat(p); err == nil {
			add(filepath.Base(p), p)
		}
	}
	// 설정 파일이 하나도 없다 = 아직 AI 프로그램을 안 붙였다. 교사가 누른 경우에만 새로 만든다.
	if create && !anyExists {
		add("Claude Desktop", mcpClientConfigPath())
	}

	out := make([]mcpResult, 0, len(targets))
	for _, t := range targets {
		changed, msg, err := installMCP(t.path, create)
		if err == nil && !changed && msg == "" {
			continue // 우리 항목이 없는 남의 설정 파일 — 조용히 넘어간다.
		}
		out = append(out, mcpResult{Name: t.name, Path: t.path, Changed: changed, Msg: msg, Err: err})
	}
	return out
}

// refreshMCPRegistration 은 앱을 켤 때 등록해 둔 곳들의 경로를 조용히 최신으로 맞춘다.
// (새 버전을 받아 파일 이름이 바뀌어도 AI 연결이 끊기지 않게.) 이미 있는 항목만 고치므로,
// 등록한 적 없는 설정 파일은 만들지도 건드리지도 않는다.
func refreshMCPRegistration() {
	for _, r := range installMCPAll(false) {
		switch {
		case r.Err != nil:
			log.Printf("AI 연결 경로 확인 실패(%s): %v", r.Path, r.Err)
		case r.Changed:
			log.Printf("AI 연결(MCP) %s — %s (%s)", r.Msg, r.Name, r.Path)
		}
	}
}

// runMCPInstall 은 `classroom-quiz.exe mcp-install [설정파일경로]` 진입점.
// 경로를 주면 그 파일에(목록에 없는 AI 프로그램용), 안 주면 이 PC 에 있는 AI 프로그램들에 등록한다.
func runMCPInstall(args []string) {
	exe, _ := selfPath()
	fmt.Println("실행 파일:", exe)

	var results []mcpResult
	if len(args) > 0 && args[0] != "" {
		changed, msg, err := installMCP(args[0], true)
		results = []mcpResult{{Name: filepath.Base(args[0]), Path: args[0], Changed: changed, Msg: msg, Err: err}}
		if err == nil {
			rememberClient(args[0]) // 다음부터 이 파일도 자동 갱신 대상
		}
	} else {
		results = installMCPAll(true)
	}

	if len(results) == 0 {
		fmt.Println("등록할 AI 프로그램 설정을 찾지 못했습니다.")
		fmt.Println("쓰시는 AI 프로그램의 설정 파일 경로를 알려 주세요:")
		fmt.Println(`  classroom-quiz.exe mcp-install "설정 파일 경로"`)
		os.Exit(1)
	}
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("  [실패] %s — %v\n", r.Name, r.Err)
			log.Printf("MCP 등록 실패(%s): %v", r.Path, r.Err)
			continue
		}
		fmt.Printf("  [%s] %s\n     %s\n", r.Name, r.Msg, r.Path)
		log.Printf("MCP 등록: %s (%s)", r.Path, exe)
	}
	fmt.Println("해당 AI 프로그램이 켜져 있으면 완전히 종료한 뒤 다시 켜세요.")
	fmt.Println("(실행 중에 설정 파일을 스스로 다시 쓰는 프로그램도 있어, 방금 등록이 덮어써질 수 있습니다.)")
	if failed == len(results) {
		os.Exit(1)
	}
}
