package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
)

// 저작(authoring) API. 학생도 같은 AP 에서 서버에 닿으므로, 퀴즈 편집 엔드포인트는
// 반드시 교사 PC 본인(localhost)에서만 허용한다. teacher-runner 의 /_admin 로컬 전용 패턴.

func localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden — 저작 화면은 교사 PC 에서만 접근할 수 있습니다.", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func handleListQuizzes(w http.ResponseWriter, r *http.Request) {
	list, err := listQuizzes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func handleGetQuiz(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q, err := loadQuiz(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "퀴즈를 찾을 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func handleSaveQuiz(w http.ResponseWriter, r *http.Request) {
	var q Quiz
	// 문항마다 내장 그림(data URI)이 붙으면 본문이 커진다. 그림 1MB × 다수 문항을 수용.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<20)).Decode(&q); err != nil {
		writeErr(w, http.StatusBadRequest, "잘못된 요청 형식입니다.")
		return
	}
	if err := saveQuiz(&q); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("퀴즈 저장: %q (%d문항) id=%s", q.Title, len(q.Questions), q.ID)
	writeJSON(w, http.StatusOK, q) // 채워진 id/updated 를 클라에 반영
}

func handleDeleteQuiz(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := deleteQuiz(id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("퀴즈 삭제: id=%s", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleMCPInstall 은 교사 화면의 [AI 연결] 버튼 처리 — 이 exe 를 AI 프로그램 설정에
// MCP 서버로 등록한다(mcpinstall.go). 실패해도 이유를 그대로 화면에 보여 준다.
func handleMCPInstall(w http.ResponseWriter, r *http.Request) {
	results := installMCPAll(true)
	if len(results) == 0 {
		writeErr(w, http.StatusBadRequest,
			"등록할 AI 프로그램 설정을 찾지 못했습니다. 쓰시는 프로그램을 설치·실행한 뒤 다시 누르거나,\n"+
				`명령 프롬프트에서 classroom-quiz.exe mcp-install "설정 파일 경로" 로 알려 주세요.`)
		return
	}
	exe, _ := selfPath()
	var done, failed []string
	for _, res := range results {
		if res.Err != nil {
			failed = append(failed, res.Name+" — "+res.Err.Error())
			continue
		}
		done = append(done, res.Name)
		log.Printf("AI 연결 등록: %s (%s)", res.Path, exe)
	}
	if len(done) == 0 {
		writeErr(w, http.StatusBadRequest, strings.Join(failed, "\n"))
		return
	}
	msg := strings.Join(done, ", ") + " 에 연결했습니다.\n해당 프로그램을 완전히 껐다가 다시 켜면 '교실 퀴즈' 도구가 붙습니다."
	if len(failed) > 0 {
		msg += "\n\n실패: " + strings.Join(failed, "\n")
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg, "command": exe})
}

// ── 게임 결과(형성평가 기록) ─────────────────────────────────────

func handleListResults(w http.ResponseWriter, r *http.Request) {
	list, err := listResults()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func handleGetResult(w http.ResponseWriter, r *http.Request) {
	res, err := loadResult(r.PathValue("quiz"), r.PathValue("file"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "결과를 찾을 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func handleDeleteResult(w http.ResponseWriter, r *http.Request) {
	if err := deleteResult(r.PathValue("quiz"), r.PathValue("file")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
