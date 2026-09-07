# classroom-quiz

같은 와이파이(AP)에 붙은 학생들이 **QR로 접속**하는 교실 실시간 버저식 퀴즈 게임 서버.
교사 PC에서 **exe 한 개를 더블클릭**하면 끝. 학생은 **앱 설치 없이 브라우저**로 들어온다.

📖 **자세한 사용법은 [사용 설명서(MANUAL.md)](MANUAL.md)** 를 참고하세요.

## 동작

1. 교사가 `classroom-quiz.exe` 더블클릭 → LAN IP 탐지 후 `0.0.0.0`에 바인딩, 서버 기동.
2. 기본 브라우저가 **교사 제어 화면**(`http://127.0.0.1:<port>/host`)을 자동으로 연다 → QR + 접속 주소 + 참가자 목록.
3. 학생은 같은 와이파이에서 **QR을 찍어** 루트(`/`)로 들어와 닉네임 입력 → 입장.
4. 입장한 학생이 교사 화면에 실시간으로 뜬다(WebSocket).

배포물은 **exe 파일 하나뿐**이다. 웹 자산(`host.html`/`student.html`)과 QR 생성은 바이너리에 내장(`//go:embed`, 순수 Go QR). 인터넷/CDN 불필요 — 학생 폰이 오프라인 LAN에서도 페이지가 정상 표시됨.

## 빌드

```bash
./build.sh                     # dist/classroom-quiz.exe (콘솔 창 보임 = 창 닫으면 종료)
./build.sh dist/quiz.exe gui   # 콘솔 없이 백그라운드 (-H windowsgui) — systray 붙이기 전엔 종료수단 없음 주의
```

리눅스에서 그대로 윈도우 exe 크로스컴파일된다(`CGO_ENABLED=0`, C 컴파일러 불필요).

로컬 실행(개발):
```bash
go run .
```

## 설계 메모

- **`0.0.0.0` 바인딩이 핵심.** 형제 프로젝트 `win_local_server`(teacher-runner)는 `127.0.0.1` 전용(소유자)이지만, 이 앱은 학생이 LAN으로 붙어야 하므로 전체 인터페이스에 바인딩한다.
- **멀티 NIC**: 유선+무선이 섞이면 LAN IP 후보가 여러 개일 수 있다. 사설망 우선순위(192.168 > 10 > 172.16-31)로 대표 IP를 고르고, 전체 후보는 `/info`에 노출(QR이 안 되면 다른 후보로 안내).
- **방화벽**: 교사 계정이 표준(non-admin)이라 `netsh`로 규칙을 못 박는다. 윈도우 기본 동작(Private 프로파일 첫 인바운드 허용)에 의존 — 대상 환경에서 실측으로 접속 정상 확인됨.
- **재접속**: 학생 페이지는 닉네임을 `sessionStorage`에 보관, 끊기면 자동 재접속(교실 와이파이 끊김 대비).

## 게임 진행

상태머신 `LOBBY → QUESTION → REVEAL → SCOREBOARD → PODIUM` (game.go). 서버가 단일
진실원천이며 모든 게임 상태 변경은 `Hub.run()` 단일 고루틴 안에서만 일어난다(락 없는 액터 모델).

- **진행**: 교사 화면(`/host`)에서 퀴즈 선택 → 시작 → 정답 공개 → 순위표 → 다음 → … → 시상대.
- **속도 점수**: `points × (1 − f/2)`, `f = 응답시간 / 제한시간`. 빠를수록 만점, 막판이면 절반.
  타이밍은 서버 기준(문제 송출 시각 vs 답안 도착 시각) — 클라 시계 불신.
- **스트릭 보너스**: 연속 정답마다 +100, 최대 +500.
- **자동 공개**: 연결된 학생 전원이 답하면 즉시 공개(타임아웃을 안 기다림).
- **답안 분포**: 공개 화면에 보기별 응답 수 막대 + 정답 표시.
- **순위표**: 누적 점수 순위 + 등수 변동(▲▼).
- **시상대**: Top3 단상 + 전체 순위. 학생은 본인 최종 등수.
- **보기 표시**: teaveloper 테마 색 + 보기 순번만큼의 엠블럼(1개·2개 나란히·3개부터 방사형), 공개/학생 화면 일관.
- **재접속**: 학생 페이지가 `sessionStorage` 토큰으로 식별 — 끊겨도 같은 점수로 복귀.
  연결(`client`)과 게임상태(`Player`)를 분리해 재접속 시 `Player.conn`만 갈아낀다.

### WebSocket 프로토콜
- 호스트 → 서버: `{type:"start",quizId}` `{type:"next"}` `{type:"end"}`
- 학생 → 서버: `{type:"answer",choice:N}`
- 서버 → 클라: `{type:"state",role,phase,...}` (호스트=전체 뷰 / 학생=개인 뷰), 오류는 `{type:"error",message}`

## AI 로 퀴즈 만들기 (MCP)

같은 exe 가 **MCP(Model Context Protocol) 서버**로도 동작한다. Claude 같은 AI 에게
"3단원으로 10문항 만들어줘" 라고 말하면 AI 가 `quizzes/` 에 퀴즈를 바로 저장하고,
교사는 `/author` 에서 다듬어 그대로 진행하면 된다.

```bash
classroom-quiz.exe mcp     # stdio(표준입출력) JSON-RPC. 포트를 열지 않는다.
```

- **교사 PC 에 파이썬·Node 를 깔지 않는다** — 배포물인 exe 자신이 MCP 서버다(별도 설치 0).
- 저장 경로·검증은 편집기와 **완전히 동일**(`saveQuiz` 재사용) → AI 가 만든 퀴즈와 손으로 만든 퀴즈가 같다.
- 도구: `list_quizzes` `get_quiz` `create_quiz` `update_quiz` `delete_quiz` `list_results` `get_result`.
- **그림은 조회 시 자리표시자로 치환**한다(base64 1MB 를 대화에 흘리지 않도록). 수정 시 그 값을
  그대로 돌려보내면 원본 그림이 유지된다.
- 앱 서버가 떠 있지 않아도 되고(파일만 다룬다), 떠 있어도 안전하다(임시파일→rename).
  단 **실행 중인 편집기 화면은 자동 갱신되지 않으니 새로고침**이 필요하다.

클라이언트 설정(교사용 Claude Desktop 등)은 [MANUAL 13. AI로 퀴즈 만들기](MANUAL.md#13-ai로-퀴즈-만들기-mcp) 참고.
이 저장소에는 Claude Code 용 [`.mcp.json`](.mcp.json) 이 들어 있다(`./build.sh` 로 `dist/classroom-quiz.exe`
를 먼저 만들어야 한다. 리눅스에서 개발 중이라면 `go build -o classroom-quiz .` 후 명령을
`./classroom-quiz` 로 바꾼다).

퀴즈/결과 폴더는 기본이 **exe 옆**이다. `CLASSROOM_QUIZ_HOME` 환경변수로 다른 폴더를 지정할 수 있다
(개발 중 실제 데이터와 분리하거나, 공유 폴더에 모을 때).

## 파일

| 파일 | 역할 |
|---|---|
| main.go | LAN IP 탐지·`0.0.0.0` 바인딩·라우팅·QR·브라우저 자동오픈·로그·`mcp` 서브커맨드 |
| mcp.go | MCP 서버(stdio JSON-RPC) — AI 가 퀴즈를 만들고 결과를 읽는 도구 |
| hub.go | 연결/디스패치(액터 run 루프), client, 재접속, 토큰 |
| game.go | 상태머신·점수·스트릭·분포·순위·시상대·뷰 빌더 |
| store.go | 퀴즈 JSON 저장소(CRUD, 검증, 원자적 쓰기) |
| author.go | 저작 API + `localOnly` 게이트 |
| results.go | 게임 결과 저장소(퀴즈별·날짜별 CRUD) |
| netmedia*.go | 학생 접속 IP의 연결 방식 판별(무선/유선) |
| assets/{host,student,author,results}.html | 교사 화면 / 학생 화면 / 편집기 / 결과 |

## 다음 후보
- 멀티정답 문항을 학생 UI에서 복수 선택(현재는 단일 탭 — 정답군 중 하나면 정답 처리)
- 참가자 페이지 닉네임 중복 처리, 비속어 필터
- systray(gui 모드) 종료 아이콘, 코드사인

## 라이선스

MIT — [LICENSE](LICENSE) 참고.
