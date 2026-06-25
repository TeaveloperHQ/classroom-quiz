// classroom-quiz — 같은 AP(LAN)에 붙은 학생들이 QR 로 접속하는 교실 퀴즈 서버.
//
// 교사 PC 에서 이 한 개 exe 를 더블클릭하면:
//   ① LAN IP 를 탐지하고 0.0.0.0 에 바인딩(학생이 붙을 수 있게 — 핵심).
//   ② 기본 브라우저로 교사 제어 화면(/host)을 자동으로 연다.
//   ③ /host 에 접속 QR + 실시간 참가자 목록이 뜬다.
//   ④ 학생은 같은 AP 에서 QR 을 찍어 / (학생 페이지)로 들어와 닉네임을 넣고 입장.
//
// 웹 자산(host.html/student.html)은 //go:embed 로 바이너리에 박혀 있어 배포 파일은 exe 하나.
//
// 빌드(리눅스에서 윈도우 exe, CGO 불필요):  ./build.sh
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	qrcode "github.com/skip2/go-qrcode"
)

//go:embed assets
var assetsFS embed.FS

// 시도할 포트 범위. preferredPort 부터 차례로 비어 있는 걸 잡는다.
const (
	preferredPort = 8080
	portTries     = 12
)

func main() {
	setupLogging()

	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		log.Fatalf("내장 자산 마운트 실패: %v", err)
	}

	primaryIP, candidates := detectLANIP()
	ln, port, err := listenLAN(preferredPort)
	if err != nil {
		log.Fatalf("포트 열기 실패: %v", err)
	}
	defer ln.Close()

	hub := newHub()
	go hub.run()

	// QR 이 가리키는 주소 = 학생 입장 URL. 학생은 같은 AP 에서 이 LAN IP 로 접속한다.
	joinURL := fmt.Sprintf("http://%s:%d/", primaryIP, port)

	media := networkMedia(primaryIP) // 학생 접속 주소가 무선망인지 유선(랜선)인지
	info := infoPayload{
		JoinURL:    joinURL,
		Port:       port,
		PrimaryIP:  primaryIP,
		Candidates: candidates,
		Media:      media,
	}

	mux := http.NewServeMux()

	// 학생 페이지 = 루트(= QR 목적지). 그 외 경로는 내장 정적 자산.
	fileServer := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			serveAsset(w, sub, "student.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	// 교사 제어 화면(브라우저 자동오픈 대상).
	mux.HandleFunc("/host", func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, sub, "host.html")
	})

	// 퀴즈 저작 화면 + API — 모두 교사 PC(localhost) 전용.
	mux.HandleFunc("/author", localOnly(func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, sub, "author.html")
	}))
	mux.HandleFunc("GET /api/quizzes", localOnly(handleListQuizzes))
	mux.HandleFunc("POST /api/quizzes", localOnly(handleSaveQuiz))
	mux.HandleFunc("GET /api/quizzes/{id}", localOnly(handleGetQuiz))
	mux.HandleFunc("DELETE /api/quizzes/{id}", localOnly(handleDeleteQuiz))

	// 게임 결과 보기/내보내기 화면 + API — 교사 PC 전용.
	mux.HandleFunc("/results", localOnly(func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, sub, "results.html")
	}))
	mux.HandleFunc("GET /api/results", localOnly(handleListResults))
	mux.HandleFunc("GET /api/results/{quiz}/{file}", localOnly(handleGetResult))
	mux.HandleFunc("DELETE /api/results/{quiz}/{file}", localOnly(handleDeleteResult))

	// 접속 QR(서버사이드 생성 — 인터넷/CDN 불필요).
	// ?size=N 으로 크기 조절(전체화면 표시 시 또렷하게). 기본 320, 128~1280 으로 제한.
	mux.HandleFunc("/qr.png", func(w http.ResponseWriter, r *http.Request) {
		size := 320
		if s := r.URL.Query().Get("size"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n >= 128 && n <= 1280 {
				size = n
			}
		}
		png, err := qrcode.Encode(joinURL, qrcode.Medium, size)
		if err != nil {
			http.Error(w, "qr error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)
	})

	// 호스트/학생 페이지가 부트스트랩 정보를 읽는 엔드포인트.
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})

	// WebSocket: ?role=host  또는  ?role=student&name=...
	mux.HandleFunc("/ws", hub.serveWS)

	log.Printf("학생접속 %s  교사화면 http://127.0.0.1:%d/host  (연결: %s)", joinURL, port, media)
	if len(candidates) > 1 {
		log.Printf("LAN IP 후보 %v (QR 가 %s 로 안 되면 다른 후보로 시도)", candidates, primaryIP)
	}

	// 교사 PC 의 브라우저를 제어 화면으로 자동으로 연다(localhost).
	go openBrowser(fmt.Sprintf("http://127.0.0.1:%d/host", port))

	srv := &http.Server{Handler: mux}
	if err := srv.Serve(ln); err != nil {
		log.Printf("서버 종료: %v", err)
	}
}

type infoPayload struct {
	JoinURL    string   `json:"joinURL"`
	Port       int      `json:"port"`
	PrimaryIP  string   `json:"primaryIP"`
	Candidates []string `json:"candidates"`
	Media      string   `json:"media"` // "wireless" | "wired" | "unknown"
}

// serveAsset 은 내장 FS 에서 html 한 장을 그대로 서빙한다.
func serveAsset(w http.ResponseWriter, sub fs.FS, name string) {
	b, err := fs.ReadFile(sub, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// listenLAN 은 0.0.0.0(전체 인터페이스)에 바인딩한다 — 학생이 LAN 으로 붙으려면 필수.
// preferredPort 가 사용 중이면 다음 포트로 넘어간다.
func listenLAN(start int) (net.Listener, int, error) {
	var lastErr error
	for p := start; p < start+portTries; p++ {
		ln, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(p))
		if err == nil {
			return ln, p, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

// detectLANIP 은 사설망 IPv4 를 골라 반환한다(우선순위 192.168 > 10 > 172.16-31).
// 두 번째 반환값은 모든 사설 후보(멀티 NIC 대비 — 유선/무선이 섞이면 교사가 골라야 할 수 있다).
func detectLANIP() (string, []string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1", []string{"127.0.0.1"}
	}
	var cands []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			cands = append(cands, ip.String())
		}
	}
	if len(cands) == 0 {
		return "127.0.0.1", []string{"127.0.0.1"}
	}
	return pickPrimary(cands), cands
}

func pickPrimary(cands []string) string {
	rank := func(ip string) int {
		switch {
		case len(ip) >= 8 && ip[:8] == "192.168.":
			return 0
		case len(ip) >= 3 && ip[:3] == "10.":
			return 1
		default: // 172.16-31.x
			return 2
		}
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if rank(c) < rank(best) {
			best = c
		}
	}
	return best
}

// openBrowser 는 OS 기본 브라우저로 url 을 연다.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("브라우저 자동 열기 실패(수동으로 %s 접속): %v", url, err)
	}
}

// setupLogging 은 exe 옆에 classroom-quiz.log 를 남긴다(콘솔이 없어도 진단 가능하게)
// + 콘솔이 있으면 화면에도 출력.
func setupLogging() {
	log.SetFlags(log.LstdFlags)
	dir := exeDir()
	f, err := os.OpenFile(filepath.Join(dir, "classroom-quiz.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}
