package main

import (
	"encoding/json"
	"log"
	"math"
	"sort"
	"time"
)

// 게임 규칙(상태머신/점수/뷰). 모든 함수는 Hub.run() 단일 고루틴에서만 호출되므로 락 불필요.

type phase string

const (
	phaseLobby      phase = "lobby"
	phaseQuestion   phase = "question"
	phaseReveal     phase = "reveal"
	phaseScoreboard phase = "scoreboard"
	phasePodium     phase = "podium"
)

// 보기별 색 — teaveloper 테마 계열(인디고/시안/퍼플 중심). 인덱스별로 학생/공개 화면이 동일하게 쓴다.
// 도형은 쓰지 않고 보기 순번만큼의 엠블럼 개수로 표시(1개·2개 나란히·3개부터 방사형) — 클라이언트가 n 으로 그린다.
var choiceColors = []string{"#6366f1", "#06b6d4", "#a855f7", "#f43f5e", "#14b8a6", "#f59e0b"}

const (
	streakStep  = 100 // 연속정답 1회당 보너스
	streakCap   = 5   // 보너스 최대 단계(+500)
	leaderTopN  = 8   // 순위표에 보여줄 상위 인원
)

type Player struct {
	id   string
	name string
	conn *client // nil 이면 연결 끊김(점수는 유지)

	score        int
	streak       int
	rank         int
	prevRank     int
	correctTotal int // 이번 판에서 맞힌 문항 수(결과 저장용)

	// 현재 문항 한정
	answered   bool
	answerIdx  int
	answerTime time.Time
	lastGain   int
	lastCorrect bool
}

// ── 호스트 명령 처리 ─────────────────────────────────────────────

func (h *Hub) onMessage(m inMsg) {
	var env struct {
		Type   string `json:"type"`
		QuizID string `json:"quizId"`
		Choice int    `json:"choice"`
	}
	if json.Unmarshal(m.data, &env) != nil {
		return
	}
	if m.c.role == "host" {
		switch env.Type {
		case "start":
			h.startGame(m.c, env.QuizID)
		case "next", "skip":
			h.advance()
		case "end":
			h.endGame()
		}
		return
	}
	// 학생
	if env.Type == "answer" {
		h.submitAnswer(m.c, env.Choice)
	}
}

func (h *Hub) startGame(host *client, quizID string) {
	q, err := loadQuiz(quizID)
	if err != nil || len(q.Questions) == 0 {
		h.sendTo(host, mustJSON(map[string]any{"type": "error", "message": "퀴즈를 불러올 수 없습니다."}))
		return
	}
	h.quiz = q
	// 점수 초기화(이미 로비에 있던 학생들 대상).
	for _, p := range h.players {
		p.score, p.streak, p.rank, p.prevRank = 0, 0, 0, 0
		p.correctTotal = 0
	}
	log.Printf("게임 시작: %q (%d문항), 참가 %d명", q.Title, len(q.Questions), len(h.players))
	h.beginQuestion(0)
}

func (h *Hub) beginQuestion(i int) {
	h.qIndex = i
	h.phase = phaseQuestion
	h.qStart = time.Now()
	for _, p := range h.players {
		p.answered = false
		p.answerIdx = -1
	}
	dur := time.Duration(h.quiz.Questions[i].TimeSec) * time.Second
	h.resetTimer(dur)
	h.broadcastState()
}

// advance — 호스트의 "다음"이 단계에 맞게 진행한다.
func (h *Hub) advance() {
	switch h.phase {
	case phaseQuestion:
		h.doReveal()
	case phaseReveal:
		h.doScoreboard()
	case phaseScoreboard:
		if h.qIndex+1 < len(h.quiz.Questions) {
			h.beginQuestion(h.qIndex + 1)
		} else {
			h.doPodium()
		}
	case phasePodium:
		h.endGame()
	}
}

func (h *Hub) onTimeUp() {
	if h.phase == phaseQuestion {
		h.doReveal()
	}
}

func (h *Hub) submitAnswer(c *client, choice int) {
	if h.phase != phaseQuestion {
		return
	}
	p, ok := h.players[c.token]
	if !ok || p.answered {
		return
	}
	q := h.quiz.Questions[h.qIndex]
	if choice < 0 || choice >= len(q.Choices) {
		return
	}
	p.answered = true
	p.answerIdx = choice
	p.answerTime = time.Now()

	// 본인에게 즉시 "제출됨" 반영 + 호스트에 답변 수 갱신.
	h.sendTo(p.conn, h.studentStateMsg(p))
	h.broadcastHost()

	// 연결된 전원이 답하면 즉시 공개.
	if h.activeCount() > 0 && h.answeredCount() >= h.activeCount() {
		h.doReveal()
	}
}

func (h *Hub) doReveal() {
	h.stopTimer()
	h.phase = phaseReveal
	q := h.quiz.Questions[h.qIndex]
	for _, p := range h.players {
		if p.answered && q.Choices[p.answerIdx].Correct {
			f := p.answerTime.Sub(h.qStart).Seconds() / float64(q.TimeSec)
			if f < 0 {
				f = 0
			}
			if f > 1 {
				f = 1
			}
			base := int(math.Round(float64(q.Points) * (1 - f/2))) // 빠를수록 만점, 막판엔 절반
			p.streak++
			bonus := minInt(p.streak-1, streakCap) * streakStep
			p.lastGain = base + bonus
			p.lastCorrect = true
			p.score += p.lastGain
			p.correctTotal++
		} else {
			p.streak = 0
			p.lastGain = 0
			p.lastCorrect = false
		}
	}
	h.broadcastState()
}

func (h *Hub) doScoreboard() {
	h.phase = phaseScoreboard
	h.rankPlayers()
	h.broadcastState()
}

func (h *Hub) doPodium() {
	h.phase = phasePodium
	h.rankPlayers()
	log.Printf("게임 종료: %d명", len(h.players))
	h.saveGameResult()
	h.broadcastState()
}

// saveGameResult 는 완료된 게임(시상대)을 results/<quizId>/<날짜>.json 으로 남긴다.
func (h *Hub) saveGameResult() {
	if h.quiz == nil || len(h.players) == 0 {
		return
	}
	res := &GameResult{
		QuizID:     h.quiz.ID,
		QuizTitle:  h.quiz.Title,
		FinishedAt: time.Now().Format(time.RFC3339),
		Questions:  len(h.quiz.Questions),
	}
	for _, p := range h.sortedPlayers() {
		res.Players = append(res.Players, ResultPlayer{
			Name: p.name, Score: p.score, Rank: p.rank, Correct: p.correctTotal,
		})
	}
	if name, err := saveResult(res); err != nil {
		log.Printf("결과 저장 실패: %v", err)
	} else {
		log.Printf("결과 저장: results/%s/%s (%d명)", h.quiz.ID, name, len(res.Players))
	}
}

func (h *Hub) endGame() {
	h.stopTimer()
	h.phase = phaseLobby
	h.quiz = nil
	h.qIndex = -1
	h.broadcastState()
}

// ── 헬퍼 ────────────────────────────────────────────────────────

func (h *Hub) resetTimer(d time.Duration) {
	h.stopTimer()
	h.timer.Reset(d)
}

func (h *Hub) stopTimer() {
	if !h.timer.Stop() {
		select {
		case <-h.timer.C:
		default:
		}
	}
}

func (h *Hub) activeCount() int {
	n := 0
	for _, p := range h.players {
		if p.conn != nil {
			n++
		}
	}
	return n
}

func (h *Hub) answeredCount() int {
	n := 0
	for _, p := range h.players {
		if p.conn != nil && p.answered {
			n++
		}
	}
	return n
}

// rankPlayers 는 점수 내림차순으로 등수를 매기고 직전 등수를 보관(변동 표시용).
func (h *Hub) sortedPlayers() []*Player {
	ps := make([]*Player, 0, len(h.players))
	for _, p := range h.players {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].score != ps[j].score {
			return ps[i].score > ps[j].score
		}
		return ps[i].name < ps[j].name
	})
	return ps
}

func (h *Hub) rankPlayers() {
	ps := h.sortedPlayers()
	for i, p := range ps {
		p.prevRank = p.rank
		p.rank = i + 1
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ── 뷰 빌더 ─────────────────────────────────────────────────────

func (h *Hub) choiceView(c Choice, idx int, withCorrect bool) map[string]any {
	m := map[string]any{
		"text":  c.Text,
		"color": choiceColors[idx%len(choiceColors)],
		"n":     idx + 1, // 보기 순번 = 표시할 엠블럼 개수
	}
	if withCorrect {
		m["correct"] = c.Correct
	}
	return m
}

func (h *Hub) hostStateMsg() []byte {
	m := map[string]any{"type": "state", "role": "host", "phase": string(h.phase)}
	switch h.phase {
	case phaseLobby:
		names := make([]string, 0, len(h.order))
		for _, tok := range h.order {
			if p := h.players[tok]; p != nil {
				names = append(names, p.name)
			}
		}
		m["players"] = names
		m["count"] = len(names)

	case phaseQuestion:
		q := h.quiz.Questions[h.qIndex]
		ch := make([]map[string]any, len(q.Choices))
		for i, c := range q.Choices {
			ch[i] = h.choiceView(c, i, false)
		}
		m["index"] = h.qIndex + 1
		m["total"] = len(h.quiz.Questions)
		m["question"] = q.Text
		m["image"] = q.Image
		m["choices"] = ch
		m["timeSec"] = q.TimeSec
		m["answers"] = h.answeredCount()
		m["players"] = h.activeCount()

	case phaseReveal:
		q := h.quiz.Questions[h.qIndex]
		ch := make([]map[string]any, len(q.Choices))
		dist := make([]int, len(q.Choices))
		for _, p := range h.players {
			if p.answered && p.answerIdx >= 0 && p.answerIdx < len(dist) {
				dist[p.answerIdx]++
			}
		}
		for i, c := range q.Choices {
			ch[i] = h.choiceView(c, i, true)
		}
		m["index"] = h.qIndex + 1
		m["total"] = len(h.quiz.Questions)
		m["question"] = q.Text
		m["image"] = q.Image
		m["choices"] = ch
		m["distribution"] = dist
		m["correctCount"] = h.correctCount()

	case phaseScoreboard, phasePodium:
		ps := h.sortedPlayers()
		lim := len(ps)
		if h.phase == phaseScoreboard && lim > leaderTopN {
			lim = leaderTopN
		}
		board := make([]map[string]any, 0, lim)
		for i := 0; i < lim; i++ {
			p := ps[i]
			delta := 0
			if p.prevRank > 0 {
				delta = p.prevRank - p.rank
			}
			board = append(board, map[string]any{
				"name": p.name, "score": p.score, "rank": p.rank, "delta": delta,
			})
		}
		m["leaderboard"] = board
		m["index"] = h.qIndex + 1
		m["total"] = len(h.quiz.Questions)
		m["hasNext"] = h.qIndex+1 < len(h.quiz.Questions)
	}
	return mustJSON(m)
}

func (h *Hub) correctCount() int {
	if h.phase != phaseReveal && h.phase != phaseScoreboard {
		return 0
	}
	q := h.quiz.Questions[h.qIndex]
	n := 0
	for _, p := range h.players {
		if p.answered && q.Choices[p.answerIdx].Correct {
			n++
		}
	}
	return n
}

func (h *Hub) studentStateMsg(p *Player) []byte {
	m := map[string]any{
		"type": "state", "role": "student", "phase": string(h.phase),
		"you": map[string]any{"name": p.name, "score": p.score, "rank": p.rank},
	}
	switch h.phase {
	case phaseQuestion:
		q := h.quiz.Questions[h.qIndex]
		ch := make([]map[string]any, len(q.Choices))
		for i, c := range q.Choices {
			ch[i] = h.choiceView(c, i, false)
		}
		m["index"] = h.qIndex + 1
		m["total"] = len(h.quiz.Questions)
		m["choices"] = ch
		m["timeSec"] = q.TimeSec
		m["answered"] = p.answered
		m["yourChoice"] = p.answerIdx

	case phaseReveal:
		q := h.quiz.Questions[h.qIndex]
		correctIdx := []int{}
		for i, c := range q.Choices {
			if c.Correct {
				correctIdx = append(correctIdx, i)
			}
		}
		m["correct"] = p.lastCorrect
		m["answered"] = p.answered
		m["gain"] = p.lastGain
		m["streak"] = p.streak
		m["correctChoices"] = correctIdx
		m["yourChoice"] = p.answerIdx

	case phaseScoreboard, phasePodium:
		delta := 0
		if p.prevRank > 0 {
			delta = p.prevRank - p.rank
		}
		m["delta"] = delta
		m["totalPlayers"] = len(h.players)
		if h.phase == phasePodium {
			m["isTop3"] = p.rank >= 1 && p.rank <= 3
		}
	}
	return mustJSON(m)
}
