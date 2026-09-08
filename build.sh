#!/usr/bin/env bash
# 교사 배포용 Windows exe 빌드. 리눅스에서 그대로 크로스컴파일된다(CGO 불필요 — 순수 Go).
#
#   ./build.sh                       # dist/classroom-quiz.exe (콘솔 창 보임 = 닫으면 종료)
#   ./build.sh dist/quiz.exe gui     # 콘솔 창 없이(-H windowsgui), 백그라운드 실행
#
# 실행 아이콘 / 파일 속성(제품 이름·버전)은 resource_windows_amd64.syso 에 들어 있다.
# Go 링커가 파일명 규칙(_windows_amd64)만 보고 자동으로 링크하므로 여기서 할 일은 없다
# (리눅스·arm64 빌드에서는 자동으로 무시된다). 빌드 도구가 없는 CI/포털에서도 그대로 붙는다.
#
# branding/app.ico 나 versioninfo.json 을 고쳤다면 아래로 .syso 를 다시 만들어 커밋한다:
#   go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest \
#     -icon=branding/app.ico -o=resource_windows_amd64.syso -64 versioninfo.json
set -euo pipefail

OUT="${1:-dist/classroom-quiz.exe}"
MODE="${2:-console}"
mkdir -p "$(dirname "$OUT")"

# 골격 단계 기본값은 console: 콘솔 창이 곧 "실행 중" 표시이자 종료 수단(창 닫기).
# 추후 systray 를 붙이면 gui 모드로 전환.
LDFLAGS="-s -w"
if [ "$MODE" = "gui" ]; then
  LDFLAGS="-H windowsgui -s -w"
fi

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "$LDFLAGS" -o "$OUT" .

echo "built: $OUT ($(du -h "$OUT" | cut -f1))  mode=$MODE"
