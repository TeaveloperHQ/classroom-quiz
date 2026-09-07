package main

// MCP(Model Context Protocol) 서버 — `classroom-quiz.exe mcp` 로 실행된다.
//
// 목적: 교사가 Claude 같은 AI 에게 "이번 단원으로 퀴즈 만들어줘" 라고 말하면
// AI 가 이 서버의 도구를 호출해 exe 옆 quizzes/*.json 에 바로 퀴즈를 만들어 준다.
// 교사는 /author 에서 다듬고 /host 에서 그대로 시작하면 된다.
//
// 설계 원칙 — 보통의 교사 PC 를 가정한다:
//   · 파이썬·Node 같은 런타임을 따로 깔지 않는다. 배포물인 exe 자신이 MCP 서버다.
//   · 저장 경로·검증 규칙은 편집기(/author)와 완전히 같다(store.go 의 saveQuiz 재사용).
//     AI 가 만든 퀴즈와 손으로 만든 퀴즈가 구분되지 않는다.
//   · 통신은 stdio(표준입출력) — 서버가 떠 있지 않아도 되고 네트워크 포트도 열지 않는다.
//
// 주의: stdout 은 JSON-RPC 전용이다. 로그는 반드시 stderr/로그파일로만 나가야 한다(log 패키지).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const (
	mcpServerName    = "classroom-quiz"
	mcpServerVersion = "0.1.0"
	mcpProtocol      = "2025-06-18"

	// 그림(base64 data URI)은 수십 KB~1MB 라 AI 에게 그대로 돌려주면 대화가 터진다.
	// 조회 시엔 이 자리표시자로 바꿔 보내고, 수정 시 이 값이 그대로 오면 원본 그림을 유지한다.
	imagePlaceholder = "(그림 있음 — 내용 생략, 수정 시 이 값 그대로 두면 그림이 유지됩니다)"
)

// ── JSON-RPC 2.0 ────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// runMCP 은 stdin 으로 들어오는 JSON-RPC 요청을 stdout 으로 응답한다(MCP stdio 전송).
func runMCP() {
	log.Printf("MCP 서버 시작 (퀴즈 폴더: %s)", quizzesDir())
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				log.Printf("MCP 서버 종료(입력 끝)")
			} else {
				log.Printf("MCP 요청을 읽지 못해 종료: %v", err)
			}
			return
		}
		resp, ok := handleRPC(&req)
		if !ok {
			continue // 알림(notification) — 응답하지 않는다.
		}
		if err := enc.Encode(resp); err != nil {
			log.Printf("MCP 응답 쓰기 실패: %v", err)
			return
		}
	}
}

func handleRPC(req *rpcRequest) (*rpcResponse, bool) {
	// id 가 없으면 알림이다(notifications/initialized 등) — 응답을 보내면 안 된다.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return nil, false
	}
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = mcpInitialize(req.Params)
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools}
	case "tools/call":
		resp.Result = mcpCallTool(req.Params)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "지원하지 않는 메서드: " + req.Method}
	}
	return resp, true
}

func mcpInitialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	// 클라이언트가 아는 버전으로 맞춰 준다(모르는 버전이면 우리 기준 버전을 제시).
	ver := mcpProtocol
	for _, known := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"} {
		if p.ProtocolVersion == known {
			ver = known
			break
		}
	}
	return map[string]any{
		"protocolVersion": ver,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": mcpServerName, "version": mcpServerVersion},
		"instructions": "교실 퀴즈(classroom-quiz) 저장소입니다. create_quiz 로 만든 퀴즈는 교사 PC 의 " +
			"quizzes 폴더에 바로 저장되어 교사 화면에서 그대로 진행할 수 있습니다.\n" +
			"퀴즈를 만들 때: 보기는 2~6개(4개 권장), 정답은 최소 1개 표시, 제한시간 기본 20초, 배점 기본 1000점.\n" +
			"학생 휴대폰 화면에서 읽히도록 질문과 보기는 짧게 쓰세요.\n" +
			"이미 실행 중인 앱의 편집기 화면은 자동 갱신되지 않으니, 저장 후 새로고침하라고 안내하세요.",
	}
}

// ── 도구 목록 ───────────────────────────────────────────────────

type mcpTool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// 문항 배열 스키마 — create_quiz / update_quiz 가 함께 쓴다.
const questionsSchema = `{
      "type": "array",
      "description": "문항 목록(1개 이상).",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string", "description": "기존 문항을 수정할 때만 지정. 새 문항이면 비워 둡니다." },
          "text": { "type": "string", "description": "질문. 학생 휴대폰에서 읽히도록 짧게." },
          "choices": {
            "type": "array",
            "description": "보기 2~6개. {text, correct} 객체 배열을 권장하며, 문자열 배열로 주면 answerIndex 로 정답을 지정하세요.",
            "items": {
              "anyOf": [
                { "type": "string" },
                {
                  "type": "object",
                  "properties": {
                    "text": { "type": "string" },
                    "correct": { "type": "boolean", "description": "정답이면 true. 문항마다 1개 이상 필요." }
                  },
                  "required": ["text"]
                }
              ]
            }
          },
          "answerIndex": { "type": "integer", "description": "정답 보기의 0-기반 번호. choices 를 문자열 배열로 준 경우에 사용." },
          "timeSec": { "type": "integer", "description": "제한시간(초). 생략 시 20." },
          "points": { "type": "integer", "description": "정답 배점. 생략 시 1000(속도 보너스는 서버가 가산)." },
          "image": { "type": "string", "description": "선택. 이미지 data URI. 수정 시 기존 그림을 유지하려면 조회 때 받은 자리표시자 문자열을 그대로 두세요." }
        },
        "required": ["text", "choices"]
      }
    }`

var mcpTools = []mcpTool{
	{
		Name:        "list_quizzes",
		Title:       "퀴즈 목록",
		Description: "교사 PC 에 저장된 퀴즈 목록(id·제목·문항 수·수정시각)을 최근 순으로 봅니다.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	},
	{
		Name:        "get_quiz",
		Title:       "퀴즈 보기",
		Description: "퀴즈 하나의 전체 내용(문항·보기·정답)을 봅니다. 그림은 용량이 커서 자리표시자로 바뀌어 나옵니다.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"퀴즈 id (list_quizzes 로 확인)"}},"required":["id"]}`),
	},
	{
		Name:  "create_quiz",
		Title: "퀴즈 만들기",
		Description: "새 퀴즈를 만들어 교사 PC 의 quizzes 폴더에 저장합니다. 저장 즉시 교사가 /author 에서 다듬고 /host 에서 시작할 수 있습니다. " +
			"보기는 문항마다 2~6개, 정답은 1개 이상 표시해야 저장됩니다.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": { "type": "string", "description": "퀴즈 제목(예: 5학년 2학기 분수의 나눗셈)." },
    "questions": ` + questionsSchema + `
  },
  "required": ["title", "questions"]
}`),
	},
	{
		Name:  "update_quiz",
		Title: "퀴즈 고치기",
		Description: "기존 퀴즈의 제목이나 문항을 바꿉니다. questions 를 주면 문항 전체가 교체되므로, 먼저 get_quiz 로 읽어 " +
			"고칠 부분만 바꾼 전체 목록을 보내세요.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": { "type": "string", "description": "고칠 퀴즈의 id." },
    "title": { "type": "string", "description": "새 제목(생략하면 그대로)." },
    "questions": ` + questionsSchema + `
  },
  "required": ["id"]
}`),
	},
	{
		Name:        "delete_quiz",
		Title:       "퀴즈 삭제",
		Description: "퀴즈 파일을 지웁니다. 되돌릴 수 없으니 교사에게 먼저 확인하세요.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
	},
	{
		Name:        "list_results",
		Title:       "게임 결과 목록",
		Description: "지난 게임(형성평가) 기록 목록을 최근 순으로 봅니다. 어떤 퀴즈를 언제 했고 몇 명이 참여했는지 알 수 있습니다.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	},
	{
		Name:        "get_result",
		Title:       "게임 결과 보기",
		Description: "지난 게임 한 판의 학생별 점수·등수·정답 수를 봅니다. 오답이 많았던 주제로 보충 퀴즈를 만들 때 씁니다.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"quiz":{"type":"string","description":"퀴즈 id (list_results 의 quizId)"},"file":{"type":"string","description":"결과 파일명 (list_results 의 file)"}},"required":["quiz","file"]}`),
	},
}

// ── 도구 호출 ───────────────────────────────────────────────────

func mcpCallTool(params json.RawMessage) any {
	var p struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return mcpErrText("잘못된 요청 형식입니다: " + err.Error())
	}

	var (
		out any
		err error
	)
	switch p.Name {
	case "list_quizzes":
		out, err = listQuizzes()
	case "get_quiz":
		out, err = toolGetQuiz(p.Args)
	case "create_quiz":
		out, err = toolCreateQuiz(p.Args)
	case "update_quiz":
		out, err = toolUpdateQuiz(p.Args)
	case "delete_quiz":
		out, err = toolDeleteQuiz(p.Args)
	case "list_results":
		out, err = listResults()
	case "get_result":
		out, err = toolGetResult(p.Args)
	default:
		return mcpErrText("알 수 없는 도구: " + p.Name)
	}
	if err != nil {
		return mcpErrText(err.Error())
	}
	return mcpText(out)
}

func mcpText(v any) map[string]any {
	s, ok := v.(string)
	if !ok {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return mcpErrText("결과를 만들지 못했습니다: " + err.Error())
		}
		s = string(b)
	}
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": s}},
	}
}

// mcpErrText 는 도구 실패를 알린다. JSON-RPC 오류가 아니라 isError 결과여야
// AI 가 메시지를 읽고 스스로 고쳐 다시 시도할 수 있다.
func mcpErrText(msg string) map[string]any {
	r := mcpText(msg)
	r["isError"] = true
	return r
}

func toolGetQuiz(args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("잘못된 인자 형식입니다: " + err.Error())
	}
	q, err := loadQuiz(in.ID)
	if err != nil {
		return nil, fmt.Errorf("퀴즈를 찾을 수 없습니다: %s", in.ID)
	}
	return hideImages(q), nil
}

func toolCreateQuiz(args json.RawMessage) (any, error) {
	var in struct {
		Title     string             `json:"title"`
		Questions []mcpQuestionInput `json:"questions"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("잘못된 인자 형식입니다: " + err.Error())
	}
	qs, err := toQuestions(in.Questions, nil)
	if err != nil {
		return nil, err
	}
	q := &Quiz{Title: strings.TrimSpace(in.Title), Questions: qs}
	if err := saveQuiz(q); err != nil {
		return nil, err
	}
	log.Printf("MCP 퀴즈 생성: %q (%d문항) id=%s", q.Title, len(q.Questions), q.ID)
	return map[string]any{
		"id":        q.ID,
		"title":     q.Title,
		"questions": len(q.Questions),
		"updated":   q.Updated,
		"file":      quizPath(q.ID),
		"note":      "저장했습니다. 교사 화면에서 퀴즈 목록을 새로고침하면 보이고, /host 에서 바로 시작할 수 있습니다.",
	}, nil
}

func toolUpdateQuiz(args json.RawMessage) (any, error) {
	var in struct {
		ID        string             `json:"id"`
		Title     *string            `json:"title"`
		Questions []mcpQuestionInput `json:"questions"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("잘못된 인자 형식입니다: " + err.Error())
	}
	cur, err := loadQuiz(in.ID)
	if err != nil {
		return nil, fmt.Errorf("퀴즈를 찾을 수 없습니다: %s", in.ID)
	}
	if in.Title != nil {
		cur.Title = strings.TrimSpace(*in.Title)
	}
	if in.Questions != nil {
		qs, err := toQuestions(in.Questions, cur)
		if err != nil {
			return nil, err
		}
		cur.Questions = qs
	}
	if err := saveQuiz(cur); err != nil {
		return nil, err
	}
	log.Printf("MCP 퀴즈 수정: %q (%d문항) id=%s", cur.Title, len(cur.Questions), cur.ID)
	return map[string]any{
		"id":        cur.ID,
		"title":     cur.Title,
		"questions": len(cur.Questions),
		"updated":   cur.Updated,
		"note":      "수정했습니다. 교사 화면이 열려 있다면 새로고침해야 반영됩니다.",
	}, nil
}

func toolDeleteQuiz(args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("잘못된 인자 형식입니다: " + err.Error())
	}
	q, err := loadQuiz(in.ID)
	if err != nil {
		return nil, fmt.Errorf("퀴즈를 찾을 수 없습니다: %s", in.ID)
	}
	if err := deleteQuiz(in.ID); err != nil {
		return nil, err
	}
	log.Printf("MCP 퀴즈 삭제: %q id=%s", q.Title, in.ID)
	return map[string]any{"deleted": in.ID, "title": q.Title}, nil
}

func toolGetResult(args json.RawMessage) (any, error) {
	var in struct {
		Quiz string `json:"quiz"`
		File string `json:"file"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("잘못된 인자 형식입니다: " + err.Error())
	}
	res, err := loadResult(in.Quiz, in.File)
	if err != nil {
		return nil, errors.New("결과를 찾을 수 없습니다.")
	}
	return res, nil
}

// ── 입력 변환 ───────────────────────────────────────────────────

// mcpChoiceInput 은 {"text":…,"correct":…} 도, 그냥 "보기 글" 문자열도 받는다
// (AI 가 어느 쪽으로 보내도 실패하지 않게).
type mcpChoiceInput struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

func (c *mcpChoiceInput) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		c.Text, c.Correct = s, false
		return nil
	}
	var o struct {
		Text    string `json:"text"`
		Correct bool   `json:"correct"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return errors.New("보기는 문자열이거나 {text, correct} 객체여야 합니다.")
	}
	c.Text, c.Correct = o.Text, o.Correct
	return nil
}

type mcpQuestionInput struct {
	ID          string           `json:"id"`
	Text        string           `json:"text"`
	Image       string           `json:"image"`
	Choices     []mcpChoiceInput `json:"choices"`
	AnswerIndex *int             `json:"answerIndex"`
	TimeSec     int              `json:"timeSec"`
	Points      int              `json:"points"`
}

// toQuestions 는 도구 인자를 저장 형식으로 바꾼다. prev 가 있으면(수정) 자리표시자로
// 온 그림을 같은 id 의 기존 그림으로 되돌린다. 나머지 검증은 saveQuiz 가 한다.
func toQuestions(in []mcpQuestionInput, prev *Quiz) ([]Question, error) {
	out := make([]Question, 0, len(in))
	for i, q := range in {
		choices := make([]Choice, 0, len(q.Choices))
		for _, c := range q.Choices {
			choices = append(choices, Choice{Text: strings.TrimSpace(c.Text), Correct: c.Correct})
		}
		if q.AnswerIndex != nil {
			idx := *q.AnswerIndex
			if idx < 0 || idx >= len(choices) {
				return nil, fmt.Errorf("%d번째 문항의 answerIndex(%d)가 보기 개수(%d)를 벗어났습니다.", i+1, idx, len(choices))
			}
			choices[idx].Correct = true
		}
		img := q.Image
		if img == imagePlaceholder {
			img = prevImage(prev, q.ID)
		}
		out = append(out, Question{
			ID:      q.ID,
			Text:    strings.TrimSpace(q.Text),
			Image:   img,
			Choices: choices,
			TimeSec: q.TimeSec,
			Points:  q.Points,
		})
	}
	return out, nil
}

func prevImage(prev *Quiz, id string) string {
	if prev == nil || id == "" {
		return ""
	}
	for _, q := range prev.Questions {
		if q.ID == id {
			return q.Image
		}
	}
	return ""
}

// hideImages 는 조회 응답에서 base64 그림을 자리표시자로 바꾼다(원본은 건드리지 않는다).
func hideImages(q *Quiz) Quiz {
	shown := *q
	shown.Questions = make([]Question, len(q.Questions))
	copy(shown.Questions, q.Questions)
	for i := range shown.Questions {
		if shown.Questions[i].Image != "" {
			shown.Questions[i].Image = imagePlaceholder
		}
	}
	return shown
}
