package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 퀴즈는 exe 옆 quizzes/<id>.json 에 한 파일씩 저장한다. DB 없이 파일로 주고받을 수
// 있어 교사 간 공유가 쉽다(다른 교사에게 .json 하나 건네면 끝).

type Choice struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

type Question struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Image   string   `json:"image,omitempty"` // 선택: data URI(예: "data:image/jpeg;base64,…"). 비면 그림 없음.
	Choices []Choice `json:"choices"`
	TimeSec int      `json:"timeSec"` // 제한시간(초)
	Points  int      `json:"points"`  // 정답 기본 배점(속도 보너스는 플레이 시 서버가 가산)
}

// 그림은 quiz JSON 안에 base64 data URI 로 내장한다(파일 하나로 공유·오프라인 유지).
// 편집기가 업로드 시 축소하지만, 서버도 방어적으로 상한을 둔다.
const maxImageBytes = 1 << 20 // data URI 문자열 1개당 1MB

// validImageDataURI 는 빈 값(그림 없음)이거나 허용된 이미지 data URI 인지 본다.
func validImageDataURI(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > maxImageBytes {
		return false
	}
	for _, p := range []string{
		"data:image/jpeg;base64,", "data:image/png;base64,",
		"data:image/webp;base64,", "data:image/gif;base64,",
	} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

type Quiz struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Questions []Question `json:"questions"`
	Updated   string     `json:"updated"` // RFC3339
}

// QuizSummary 는 목록 표시용 경량 정보.
type QuizSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Questions int    `json:"questions"`
	Updated   string `json:"updated"`
}

func quizzesDir() string {
	return filepath.Join(exeDir(), "quizzes")
}

func quizPath(id string) string {
	return filepath.Join(quizzesDir(), id+".json")
}

// validQuizID 는 경로 조작을 막는다(파일명에 들어갈 안전한 문자만).
func validQuizID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

func listQuizzes() ([]QuizSummary, error) {
	entries, err := os.ReadDir(quizzesDir())
	if errors.Is(err, os.ErrNotExist) {
		return []QuizSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]QuizSummary, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(quizzesDir(), e.Name()))
		if err != nil {
			continue
		}
		var q Quiz
		if json.Unmarshal(b, &q) != nil {
			continue
		}
		out = append(out, QuizSummary{
			ID: q.ID, Title: q.Title, Questions: len(q.Questions), Updated: q.Updated,
		})
	}
	// 최근 수정 순.
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out, nil
}

func loadQuiz(id string) (*Quiz, error) {
	if !validQuizID(id) {
		return nil, errors.New("잘못된 퀴즈 ID")
	}
	b, err := os.ReadFile(quizPath(id))
	if err != nil {
		return nil, err
	}
	var q Quiz
	if err := json.Unmarshal(b, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

// saveQuiz 는 검증 후 원자적으로(temp→rename) 기록한다. id/문항 id 가 비어 있으면 채운다.
func saveQuiz(q *Quiz) error {
	if strings.TrimSpace(q.Title) == "" {
		return errors.New("퀴즈 제목을 입력하세요.")
	}
	if len(q.Questions) == 0 {
		return errors.New("문항을 최소 1개 추가하세요.")
	}
	for i := range q.Questions {
		qu := &q.Questions[i]
		if strings.TrimSpace(qu.Text) == "" {
			return errors.New("빈 문항이 있습니다.")
		}
		if !validImageDataURI(qu.Image) {
			return errors.New("그림 형식이 올바르지 않거나 너무 큽니다(1MB 이하 이미지만).")
		}
		// 보기 중 빈 칸 제거.
		clean := qu.Choices[:0]
		for _, c := range qu.Choices {
			if strings.TrimSpace(c.Text) != "" {
				clean = append(clean, c)
			}
		}
		qu.Choices = clean
		if len(qu.Choices) < 2 {
			return errors.New("각 문항에 보기를 2개 이상 입력하세요.")
		}
		hasCorrect := false
		for _, c := range qu.Choices {
			if c.Correct {
				hasCorrect = true
			}
		}
		if !hasCorrect {
			return errors.New("각 문항에 정답을 1개 이상 표시하세요.")
		}
		if qu.ID == "" {
			qu.ID = newID()
		}
		if qu.TimeSec <= 0 {
			qu.TimeSec = 20
		}
		if qu.Points <= 0 {
			qu.Points = 1000
		}
	}
	if q.ID == "" {
		q.ID = newID()
	}
	if !validQuizID(q.ID) {
		return errors.New("잘못된 퀴즈 ID")
	}
	q.Updated = time.Now().Format(time.RFC3339)

	if err := os.MkdirAll(quizzesDir(), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return err
	}
	tmp := quizPath(q.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, quizPath(q.ID))
}

func deleteQuiz(id string) error {
	if !validQuizID(id) {
		return errors.New("잘못된 퀴즈 ID")
	}
	return os.Remove(quizPath(id))
}
