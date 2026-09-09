package main

import (
	"encoding/json"
	"testing"
	"time"
)

// Hub 는 단일 고루틴 액터라, run() 없이 핸들러를 직접 불러 그대로 검증할 수 있다.
// (교실에서 실제로 났던 버그 두 가지를 고정한다: 명단에 남는 옛 이름, 튀는 타이머)

// fakeClient 는 WebSocket 없이 전송 채널만 가진 연결. sendTo 는 채널이 막히면 넘어가므로
// 실제 소켓 없이도 안전하다.
func fakeClient(role, token, name string) *client {
	return &client{send: make(chan []byte, 16), role: role, token: token, name: name}
}

func hostView(t *testing.T, h *Hub) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(h.hostStateMsg(), &m); err != nil {
		t.Fatalf("호스트 상태 JSON 파싱 실패: %v", err)
	}
	return m
}

func rosterNames(t *testing.T, h *Hub) []string {
	t.Helper()
	raw, _ := hostView(t, h)["players"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

func sampleQuiz(t *testing.T, timeSec int) *Quiz {
	t.Helper()
	t.Setenv("CLASSROOM_QUIZ_HOME", t.TempDir())
	q := &Quiz{
		Title: "테스트 퀴즈",
		Questions: []Question{{
			Text:    "1 + 1 = ?",
			Choices: []Choice{{Text: "2", Correct: true}, {Text: "3"}},
			TimeSec: timeSec,
			Points:  1000,
		}},
	}
	if err := saveQuiz(q); err != nil {
		t.Fatalf("퀴즈 저장 실패: %v", err)
	}
	return q
}

// 로비에서 나간 학생은 명단에서 사라져야 한다(지킬 점수가 없다).
// 남겨 두면 교사 화면에 나간 학생의 이름이 계속 붙어 있다.
func TestLobbyRosterDropsDisconnected(t *testing.T) {
	h := newHub()
	a := fakeClient("student", "tok-a", "민준")
	b := fakeClient("student", "tok-b", "서연")
	h.onRegister(a)
	h.onRegister(b)

	if got := rosterNames(t, h); len(got) != 2 {
		t.Fatalf("입장 직후 명단 = %v, 2명이어야 한다", got)
	}

	h.onUnregister(b)

	got := rosterNames(t, h)
	if len(got) != 1 || got[0] != "민준" {
		t.Errorf("나간 뒤 명단 = %v, [민준] 이어야 한다", got)
	}
	if cnt, _ := hostView(t, h)["count"].(float64); int(cnt) != 1 {
		t.Errorf("인원 수 = %v, 명단과 같아야 한다", cnt)
	}
}

// 이름을 바꿔 다시 들어오면 옛 이름이 남으면 안 된다(같은 기기 = 같은 토큰).
func TestRenameLeavesNoGhost(t *testing.T) {
	h := newHub()
	old := fakeClient("student", "tok-a", "민준")
	h.onRegister(old)
	h.onUnregister(old)
	h.onRegister(fakeClient("student", "tok-a", "민준이"))

	got := rosterNames(t, h)
	if len(got) != 1 || got[0] != "민준이" {
		t.Errorf("이름 변경 후 명단 = %v, [민준이] 하나여야 한다", got)
	}
}

// 게임 중 QR 을 다시 찍어 들어오면(새 탭 = 새 토큰) 같은 이름의 끊긴 학생을 이어받아야 한다.
// 안 그러면 점수를 잃고 명단엔 옛 이름이 유령으로 남는다.
func TestMidGameRejoinKeepsScore(t *testing.T) {
	q := sampleQuiz(t, 30)
	h := newHub()
	host := fakeClient("host", "", "")
	h.onRegister(host)
	s := fakeClient("student", "tok-a", "민준")
	h.onRegister(s)

	h.startGame(host, q.ID)
	h.submitAnswer(s, 0) // 정답
	h.doReveal()

	score := h.players["tok-a"].score
	if score <= 0 {
		t.Fatalf("정답 점수 = %d, 0보다 커야 한다", score)
	}

	h.onUnregister(s)                                  // 화면이 꺼져 연결이 끊김
	h.onRegister(fakeClient("student", "tok-b", "민준")) // QR 다시 찍어 새 탭으로 입장

	if len(h.players) != 1 {
		t.Fatalf("플레이어 수 = %d, 이어받아 1명이어야 한다(중복 생성됨)", len(h.players))
	}
	p, ok := h.players["tok-b"]
	if !ok {
		t.Fatal("새 토큰으로 이어받지 못했다")
	}
	if p.score != score {
		t.Errorf("이어받은 점수 = %d, 원래 %d 여야 한다", p.score, score)
	}
	if p.conn == nil {
		t.Error("이어받은 뒤 연결이 붙어 있어야 한다")
	}
}

// 남은 시간은 서버가 정한다 — 상태를 다시 보내도 제한시간으로 되돌아가면 안 된다(타이머 튐).
func TestRemainMsCountsDown(t *testing.T) {
	q := sampleQuiz(t, 5)
	h := newHub()
	host := fakeClient("host", "", "")
	h.onRegister(host)
	s := fakeClient("student", "tok-a", "민준")
	h.onRegister(s)
	h.startGame(host, q.ID)

	first, _ := hostView(t, h)["remainMs"].(float64)
	if first <= 4000 || first > 5000 {
		t.Fatalf("시작 직후 남은 시간 = %vms, 5000 근처여야 한다", first)
	}

	time.Sleep(1100 * time.Millisecond)

	// 학생이 답해 상태가 다시 나가는 상황과 같다 — 여기서 값이 되돌아가면 화면 숫자가 튄다.
	second, _ := hostView(t, h)["remainMs"].(float64)
	if second >= first-900 {
		t.Errorf("1.1초 뒤 남은 시간 = %vms, %vms 보다 확실히 줄어야 한다", second, first)
	}
	if second <= 0 {
		t.Errorf("남은 시간 = %vms, 아직 0보다 커야 한다", second)
	}

	var sv map[string]any
	if err := json.Unmarshal(h.studentStateMsg(h.players["tok-a"]), &sv); err != nil {
		t.Fatalf("학생 상태 JSON 파싱 실패: %v", err)
	}
	if _, ok := sv["remainMs"]; !ok {
		t.Error("학생 화면에도 remainMs 가 가야 한다")
	}
}
