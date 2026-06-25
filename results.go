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

// 게임 결과는 exe 옆 results/<quizId>/<날짜시각>.json 에 한 판씩 저장한다.
// 퀴즈별 폴더 + 날짜 파일이라 "어느 퀴즈를 언제 했나"가 폴더 구조로 바로 보인다.
// 그림 내장 퀴즈처럼, 결과도 파일이라 그대로 백업·공유·이관이 쉽다.

type ResultPlayer struct {
	Name    string `json:"name"`
	Score   int    `json:"score"`
	Rank    int    `json:"rank"`
	Correct int    `json:"correct"` // 맞힌 문항 수
}

type GameResult struct {
	QuizID     string         `json:"quizId"`
	QuizTitle  string         `json:"quizTitle"`
	FinishedAt string         `json:"finishedAt"` // RFC3339
	Questions  int            `json:"questions"`
	Players    []ResultPlayer `json:"players"`
}

// ResultSummary 는 목록 표시용 경량 정보.
type ResultSummary struct {
	QuizID     string `json:"quizId"`
	QuizTitle  string `json:"quizTitle"`
	FinishedAt string `json:"finishedAt"`
	File       string `json:"file"` // quizId 폴더 안의 파일명
	Players    int    `json:"players"`
	TopName    string `json:"topName"`
	TopScore   int    `json:"topScore"`
}

func resultsDir() string { return filepath.Join(exeDir(), "results") }

// validResultFile 은 경로 조작을 막는다(폴더 안 단일 .json 파일명만 허용).
func validResultFile(name string) bool {
	if name == "" || len(name) > 64 || !strings.HasSuffix(name, ".json") || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// saveResult 는 results/<quizId>/<날짜시각>.json 에 원자적으로 기록하고 파일명을 돌려준다.
func saveResult(r *GameResult) (string, error) {
	if !validQuizID(r.QuizID) {
		return "", errors.New("잘못된 퀴즈 ID")
	}
	dir := filepath.Join(resultsDir(), r.QuizID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	name := time.Now().Format("2006-01-02_150405") + ".json"
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	return name, nil
}

func listResults() ([]ResultSummary, error) {
	root := resultsDir()
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []ResultSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []ResultSummary{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		qid := d.Name()
		files, err := os.ReadDir(filepath.Join(root, qid))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, qid, f.Name()))
			if err != nil {
				continue
			}
			var r GameResult
			if json.Unmarshal(b, &r) != nil {
				continue
			}
			var top ResultPlayer
			for _, p := range r.Players {
				if p.Rank == 1 {
					top = p
					break
				}
			}
			out = append(out, ResultSummary{
				QuizID: qid, QuizTitle: r.QuizTitle, FinishedAt: r.FinishedAt,
				File: f.Name(), Players: len(r.Players), TopName: top.Name, TopScore: top.Score,
			})
		}
	}
	// 최근 종료 순.
	sort.Slice(out, func(i, j int) bool { return out[i].FinishedAt > out[j].FinishedAt })
	return out, nil
}

func loadResult(quizID, file string) (*GameResult, error) {
	if !validQuizID(quizID) || !validResultFile(file) {
		return nil, errors.New("잘못된 경로")
	}
	b, err := os.ReadFile(filepath.Join(resultsDir(), quizID, file))
	if err != nil {
		return nil, err
	}
	var r GameResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func deleteResult(quizID, file string) error {
	if !validQuizID(quizID) || !validResultFile(file) {
		return errors.New("잘못된 경로")
	}
	return os.Remove(filepath.Join(resultsDir(), quizID, file))
}
