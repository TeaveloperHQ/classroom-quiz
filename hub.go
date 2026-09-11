package main

import (
	"crypto/rand"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// 연결/디스패치 레이어. 모든 게임 상태 변경은 run() 단일 고루틴 안에서만 일어나므로
// 락이 필요 없다(액터 모델). 게임 규칙은 game.go 에 있다.

const (
	writeWait = 10 * time.Second
	// 끊긴 연결을 알아채는 데 걸리는 시간. 이 시간만큼은 그 학생의 이름이 잡혀 있어
	// 다시 들어오려는 본인이 "이미 참가 중"이라는 말을 들을 수 있으므로 짧게 잡는다.
	pongWait   = 30 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // 로컬 교실 전용
}

// client 는 하나의 WebSocket 연결. 학생의 게임 상태(점수 등)는 Player 에 있고
// client 는 순수 전송 계층이다(재접속 시 Player.conn 만 새 client 로 갈아낀다).
type client struct {
	hub   *Hub
	conn  *websocket.Conn
	send  chan []byte
	role  string // "host" | "student"
	token string // 학생 식별 토큰(재접속용)
	name  string
}

type inMsg struct {
	c    *client
	data []byte
}

type Hub struct {
	register   chan *client
	unregister chan *client
	inbound    chan inMsg

	hosts   map[*client]bool
	players map[string]*Player // 토큰 → 플레이어
	order   []string           // 입장 순서(안정적 나열용)

	// 게임 상태
	quiz   *Quiz
	phase  phase
	qIndex int
	qStart time.Time
	qEnd   time.Time // 현재 문항의 마감 시각 — 남은 시간은 서버가 정한다(클라 카운트다운은 표시용)
	timer  *time.Timer
}

func newHub() *Hub {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return &Hub{
		register:   make(chan *client),
		unregister: make(chan *client),
		inbound:    make(chan inMsg, 64),
		hosts:      make(map[*client]bool),
		players:    make(map[string]*Player),
		phase:      phaseLobby,
		qIndex:     -1,
		timer:      t,
	}
}

func (h *Hub) run() {
	for {
		select {
		case c := <-h.register:
			h.onRegister(c)
		case c := <-h.unregister:
			h.onUnregister(c)
		case m := <-h.inbound:
			h.onMessage(m)
		case <-h.timer.C:
			h.onTimeUp()
		}
	}
}

func (h *Hub) onRegister(c *client) {
	if c.role == "host" {
		h.hosts[c] = true
		h.sendTo(c, h.hostStateMsg())
		return
	}
	// 학생: 토큰으로 기존 플레이어를 찾으면 재접속(점수 유지), 없으면 신규.
	p, ok := h.players[c.token]
	if !ok {
		// 토큰이 다른데(예: QR 을 다시 찍어 새 탭에서 들어옴 — 탭마다 세션이 새로 생긴다)
		// 같은 이름으로 끊겨 있던 학생이면 이어받는다. 안 그러면 점수를 잃고 명단에
		// 옛 이름이 유령으로 남는다.
		if old := h.findDisconnectedByName(c.name); old != nil {
			h.retoken(old, c.token)
			p, ok = old, true
			log.Printf("학생 재입장(이름으로 이어받음): %s", p.name)
		} else if busy := h.findConnectedByName(c.name); busy != nil {
			// 같은 이름이 지금도 붙어 있다. 옛 탭을 열어 둔 채 QR 을 다시 찍은 본인이거나,
			// 별명이 겹친 다른 학생이다. 여기서 새 플레이어를 만들어 주면 점수가 둘로
			// 갈리므로(교실에서 실제로 일어났다) 들여보내지 않고 이유를 알려 준다.
			h.sendTo(c, mustJSON(map[string]any{
				"type": "error",
				"message": "이미 참가 중인 이름입니다. 먼저 들어간 화면에서 계속하세요.\n" +
					"그 화면을 닫았다면 30초쯤 뒤에 다시 들어오면 이어서 할 수 있습니다.\n" +
					"다른 사람이라면 다른 이름으로 들어오세요.",
			}))
			log.Printf("입장 거절(이름 중복): %s", c.name)
			return
		}
	}
	if ok {
		p.conn = c
		if c.name != "" {
			p.name = c.name
		}
		log.Printf("학생 재접속: %s", p.name)
	} else {
		p = &Player{id: c.token, name: c.name, conn: c}
		h.players[c.token] = p
		h.order = append(h.order, c.token)
		log.Printf("학생 입장: %s — 총 %d명", p.name, len(h.players))
	}
	h.sendTo(c, h.studentStateMsg(p))
	h.broadcastHost() // 호스트 인원/로스터 갱신
}

func (h *Hub) onUnregister(c *client) {
	if c.role == "host" {
		delete(h.hosts, c)
		close(c.send)
		return
	}
	// 게임 중에는 점수 보존을 위해 삭제하지 않고 연결만 끊는다(재접속 대비).
	// 로비에서는 지킬 점수가 없으므로 명단에서 아예 뺀다 — 남겨 두면 나간 학생이나
	// 이름을 바꿔 다시 들어온 학생의 옛 이름이 참가자 명단에 계속 남는다.
	if p, ok := h.players[c.token]; ok && p.conn == c {
		p.conn = nil
		close(c.send)
		if h.phase == phaseLobby {
			h.removePlayer(c.token)
			log.Printf("학생 나감: %s — 총 %d명", p.name, len(h.players))
		} else {
			log.Printf("학생 연결 끊김: %s", p.name)
		}
		h.broadcastHost()
	} else {
		close(c.send)
	}
}

// findDisconnectedByName 은 같은 이름으로 연결이 끊긴 플레이어를 찾는다(재입장 이어받기용).
func (h *Hub) findDisconnectedByName(name string) *Player {
	return h.findByName(name, false)
}

// findConnectedByName 은 같은 이름으로 지금 붙어 있는 플레이어를 찾는다(중복 입장 거절용).
func (h *Hub) findConnectedByName(name string) *Player {
	return h.findByName(name, true)
}

// findByName — 이름 비교는 앞뒤 공백과 대소문자를 무시한다(학생이 다시 칠 때 흔한 차이).
func (h *Hub) findByName(name string, connected bool) *Player {
	want := normName(name)
	if want == "" {
		return nil
	}
	for _, tok := range h.order {
		p := h.players[tok]
		if p == nil || (p.conn != nil) != connected {
			continue
		}
		if normName(p.name) == want {
			return p
		}
	}
	return nil
}

func normName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// retoken 은 플레이어를 새 토큰으로 옮긴다(같은 사람이 새 탭/기기로 다시 들어온 경우).
func (h *Hub) retoken(p *Player, newToken string) {
	old := p.id
	if old == newToken {
		return
	}
	delete(h.players, old)
	p.id = newToken
	h.players[newToken] = p
	for i, tok := range h.order {
		if tok == old {
			h.order[i] = newToken
			return
		}
	}
	h.order = append(h.order, newToken)
}

// removePlayer 는 명단에서 완전히 지운다(로비에서 나갔을 때만 — 게임 중엔 점수를 지켜야 한다).
func (h *Hub) removePlayer(token string) {
	delete(h.players, token)
	for i, tok := range h.order {
		if tok == token {
			h.order = append(h.order[:i], h.order[i+1:]...)
			return
		}
	}
}

// sendTo 는 막히면 버리지 않고 넘어간다(다음 상태 브로드캐스트가 전체를 재전송하므로 복구됨).
func (h *Hub) sendTo(c *client, b []byte) {
	if c == nil {
		return
	}
	select {
	case c.send <- b:
	default:
	}
}

func (h *Hub) broadcastHost() {
	b := h.hostStateMsg()
	for hc := range h.hosts {
		h.sendTo(hc, b)
	}
}

// broadcastState 는 호스트(전체 뷰)와 각 학생(개인 뷰)에게 현재 상태를 보낸다.
func (h *Hub) broadcastState() {
	h.broadcastHost()
	for _, p := range h.players {
		if p.conn != nil {
			h.sendTo(p.conn, h.studentStateMsg(p))
		}
	}
}

// serveWS — ?role=host  또는  ?role=student&name=..&token=..
func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	role := q.Get("role")
	if role != "host" {
		role = "student"
	}
	name := q.Get("name")
	token := q.Get("token")
	if role == "student" {
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if token == "" {
			token = newID() // 클라가 토큰을 안 주면 생성(재접속 추적은 약화됨)
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS 업그레이드 실패: %v", err)
		return
	}
	c := &client{hub: h, conn: conn, send: make(chan []byte, 32), role: role, token: token, name: name}
	h.register <- c
	go c.writePump()
	go c.readPump()
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		c.hub.inbound <- inMsg{c: c, data: msg}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	const hexd = "0123456789abcdef"
	out := make([]byte, 12)
	for i, v := range b {
		out[i*2] = hexd[v>>4]
		out[i*2+1] = hexd[v&0x0f]
	}
	return string(out)
}
