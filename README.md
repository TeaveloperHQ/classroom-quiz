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

**실행 아이콘·파일 속성**은 `resource_windows_amd64.syso` 로 들어간다. Go 는 `branding/app.ico`
같은 이미지 파일을 스스로 넣지 않으므로, 아이콘·버전 정보를 담은 리소스 오브젝트를 만들어
저장소에 커밋해 둔다 — 링커가 **파일명 규칙(`_windows_amd64`)만 보고 자동 링크**하므로
빌드 명령이나 CI/포털 설정을 건드릴 필요가 없고, 리눅스·arm64 빌드에서는 자동으로 무시된다.
`branding/app.ico` 나 `versioninfo.json` 을 고치면 다시 만들어 커밋한다:

```bash
go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest \
  -icon=branding/app.ico -o=resource_windows_amd64.syso -64 versioninfo.json
```

넣었는지 확인: exe 에 `.rsrc` 섹션이 있으면 된다. 윈도우에서는
`(Get-Item dist\classroom-quiz.exe).VersionInfo` 로 제품 이름·버전이 보인다.

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
- **학생 화면**: 질문 글 + 보기(엠블럼·색·글). 질문은 최대 4줄까지만 보여 주고(짧은 화면은 2줄),
  그림은 교사 화면에만 — 폰에서 탭 영역이 밀리지 않게.
- **진행 중 QR**: 헤더 [QR 보기] 로 언제든 접속 QR 을 크게 띄운다(튕긴 학생 재입장용). 진행 중에는
  참가자 명단 대신 "쓰던 닉네임 그대로" 안내를 띄운다. 타이머는 뒤에서 계속 흐른다.
- **재접속**: 학생 페이지가 `sessionStorage` 토큰으로 식별 — 끊겨도 같은 점수로 복귀.
  연결(`client`)과 게임상태(`Player`)를 분리해 재접속 시 `Player.conn`만 갈아낀다.
  QR 을 다시 찍으면 탭이 새로 열려 토큰도 새로 생기므로, **게임 중에는 같은 이름의 끊긴 플레이어를
  이어받는다**. 로비에서 끊기면(지킬 점수가 없다) 명단에서 지운다 — 안 그러면 나가거나 이름을 바꾼
  학생의 옛 이름이 유령으로 남는다.
- **타이머**: 남은 시간의 기준은 서버(`remainMs`). 상태는 학생이 답할 때마다 다시 나가므로, 클라가
  제한시간부터 새로 세면 숫자가 되돌아가 튄다. 화면은 받은 값으로 마감 시각을 잡아 그린다.

### WebSocket 프로토콜
- 호스트 → 서버: `{type:"start",quizId}` `{type:"next"}` `{type:"end"}`
- 학생 → 서버: `{type:"answer",choice:N}`
- 서버 → 클라: `{type:"state",role,phase,...}` (호스트=전체 뷰 / 학생=개인 뷰), 오류는 `{type:"error",message}`

## AI 로 퀴즈 만들기 (MCP)

같은 exe 가 **MCP(Model Context Protocol) 서버**로도 동작한다. Claude 같은 AI 에게
"3단원으로 10문항 만들어줘" 라고 말하면 AI 가 `quizzes/` 에 퀴즈를 바로 저장하고,
교사는 `/author` 에서 다듬어 그대로 진행하면 된다.

```bash
classroom-quiz.exe mcp                    # stdio(표준입출력) JSON-RPC. 포트를 열지 않는다.
classroom-quiz.exe mcp-install [설정경로]   # 이 exe 를 AI 프로그램 설정에 등록(교사 화면 [AI 연결] 과 동일)
```

**경로 등록은 exe 가 스스로 한다.** 배포 파일 이름에는 버전·커밋 해시가 붙어
(`교실_퀴즈-0.0.0-abc1234.exe`) 빌드마다 달라지므로, 사람이 설정 파일 경로를 관리하면 매번 깨진다.
교사 화면의 **[AI 연결]** 버튼(또는 `mcp-install`)이 제 절대경로를 적는다. **누가 어떤 프로그램을
쓸지 모르므로 특정 클라이언트를 가정하지 않는다** — 알려진 자리(Claude Desktop / Claude Code
`~/.claude.json` / Cursor)를 훑되 **그 PC 에 실제로 깔려 있는 것만** 건드린다. 판단 기준은 설치·데이터
폴더(`%LOCALAPPDATA%\AnthropicClaude`, `~/.claude`, `~/.cursor` 등)와 기존 설정 파일이다.
**우리가 만들어 둔 설정만 남아 있는 폴더는 설치로 치지 않는다** — 안 그러면 앱을 켤 때마다 없는
프로그램을 손봤다는 로그가 찍힌다. 하나도 못 찾으면 아무것도 만들지 않고 "찾지 못했다"고 알린다
(Claude Desktop 은 설정 파일을 스스로 만들지 않으므로, 설치돼 있으면 설정이 없어도 만들어 준다).
목록에 없는 프로그램은 `mcp-install <경로>` 로 알려 주면 그 경로를 데이터 폴더의
`mcp-clients.json` 에 기억해 함께 관리한다.

그 뒤로는 **앱을 켤 때마다 이미 등록된 항목의 경로만 조용히 갱신**한다(`refreshMCPRegistration`).
등록한 적이 없으면 아무것도 만들지 않는다 — 남의 설정 파일이므로 다른 서버 설정과 우리 항목의
다른 필드(`type`·`env` 등)는 보존하고, 고치기 전 `.bak` 을 남기며, 깨진 JSON 은 덮어쓰지 않고
오류로 알린다. 실행 중에 제 설정 파일을 다시 쓰는 클라이언트(Claude Code 등)가 있어, 등록은
그 프로그램을 끈 상태에서 하라고 안내한다.

- **교사 PC 에 파이썬·Node 를 깔지 않는다** — 배포물인 exe 자신이 MCP 서버다(별도 설치 0).
- 저장 경로·검증은 편집기와 **완전히 동일**(`saveQuiz` 재사용) → AI 가 만든 퀴즈와 손으로 만든 퀴즈가 같다.
- 도구: `list_quizzes` `get_quiz` `create_quiz` `update_quiz` `delete_quiz` `list_results` `get_result`.
- **그림은 조회 시 자리표시자로 치환**한다(base64 1MB 를 대화에 흘리지 않도록). 수정 시 그 값을
  그대로 돌려보내면 원본 그림이 유지된다.
- 앱 서버가 떠 있지 않아도 되고(파일만 다룬다), 떠 있어도 안전하다(임시파일→rename).
- **화면이 알아서 따라온다**: 로비는 4초마다 퀴즈 목록을 다시 읽고, 편집기는 창이 포커스를 받을 때
  다시 읽는다 — AI 가 만든 퀴즈를 보려고 교사가 새로고침할 일이 없다(교사는 F5 를 모른다).
- **MCP 모드에서는 콘솔 창을 숨긴다**(`console_windows.go`). AI 프로그램(GUI)이 자식 프로세스로
  띄우면 검은 창이 뜰 수 있는데, 교사가 그 창을 닫으면 연결이 끊긴다. 명령 프롬프트에서 직접
  실행한 경우에는(콘솔에 다른 프로세스가 붙어 있음) 남의 창이므로 건드리지 않는다.

클라이언트 설정(교사용 Claude Desktop 등)은 [MANUAL 13. AI로 퀴즈 만들기](MANUAL.md#13-ai로-퀴즈-만들기-mcp) 참고.
이 저장소에는 Claude Code 용 [`.mcp.json`](.mcp.json) 이 들어 있다(`./build.sh` 로 `dist/classroom-quiz.exe`
를 먼저 만들어야 한다. 리눅스에서 개발 중이라면 `go build -o classroom-quiz .` 후 명령을
`./classroom-quiz` 로 바꾼다).

퀴즈/결과 폴더는 기본이 **exe 옆**이다. `CLASSROOM_QUIZ_HOME` 환경변수로 다른 폴더를 지정할 수 있다
(개발 중 실제 데이터와 분리하거나, 공유 폴더에 모을 때).

## 파일

| 파일 | 역할 |
|---|---|
| main.go | LAN IP 탐지·`0.0.0.0` 바인딩·라우팅·QR·브라우저 자동오픈·로그·`mcp`/`mcp-install` 서브커맨드 |
| mcp.go | MCP 서버(stdio JSON-RPC) — AI 가 퀴즈를 만들고 결과를 읽는 도구 |
| mcpinstall.go | AI 프로그램 설정에 이 exe 를 등록·경로 자동 갱신(파일 이름이 바뀌어도 연결 유지) |
| hub.go | 연결/디스패치(액터 run 루프), client, 재접속, 토큰 |
| game.go | 상태머신·점수·스트릭·분포·순위·시상대·뷰 빌더 |
| store.go | 퀴즈 JSON 저장소(CRUD, 검증, 원자적 쓰기) |
| author.go | 저작 API + `localOnly` 게이트 |
| results.go | 게임 결과 저장소(퀴즈별·날짜별 CRUD) |
| netmedia*.go | 학생 접속 IP의 연결 방식 판별(무선/유선) |
| console_*.go | MCP 모드에서 콘솔 창 숨기기(윈도우 전용, 남의 콘솔은 건드리지 않음) |
| versioninfo.json + resource_windows_amd64.syso | 윈도우 실행 아이콘·파일 속성(제품 이름·버전) 리소스 |
| hub_test.go, mcpinstall_test.go | 명단·재접속·타이머, MCP 등록 회귀 테스트 |
| assets/{host,student,author,results}.html | 교사 화면 / 학생 화면 / 편집기 / 결과 |

## 다음 후보
- 멀티정답 문항을 학생 UI에서 복수 선택(현재는 단일 탭 — 정답군 중 하나면 정답 처리)
- 참가자 페이지 닉네임 중복 처리, 비속어 필터
- systray(gui 모드) 종료 아이콘, 코드사인

## 라이선스

MIT — [LICENSE](LICENSE) 참고.
