package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

type clientMsg struct {
	Action  string `json:"action"`
	MatchID string `json:"matchId"`
	Tick    int    `json:"tick"`
	Speed   string `json:"multiplier"`
}

func (srv *server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	// Expect OBSERVE as the first message (with a generous deadline).
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, raw, err := conn.ReadMessage()
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}

	var msg clientMsg
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Action != "OBSERVE" || msg.MatchID == "" {
		errPayload, _ := json.Marshal(map[string]string{"code": "bad_request", "message": "first message must be OBSERVE with matchId"})
		conn.WriteMessage(websocket.TextMessage, makeWSEvent("ERROR", json.RawMessage(errPayload)))
		return
	}
	matchID := msg.MatchID

	// Wait for the match runner to create the liveMatch (up to 20 s for WASM compile+load).
	lm := srv.waitForLiveMatch(matchID, 20*time.Second)
	if lm == nil {
		errPayload, _ := json.Marshal(map[string]string{"code": "match_not_found", "message": "match not found or did not start in time"})
		conn.WriteMessage(websocket.TextMessage, makeWSEvent("ERROR", json.RawMessage(errPayload)))
		return
	}

	// Drain incoming messages in a goroutine so conn pings are answered.
	// We don't need to act on REPLAY_SEEK/SPEED here — they only affect
	// the streaming index which is managed locally below.
	connDone := make(chan struct{})
	go func() {
		defer close(connDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Send initial MATCH_SNAPSHOT.
	lm.mu.Lock()
	snapshot := lm.snapshot
	lm.mu.Unlock()
	if err := conn.WriteMessage(websocket.TextMessage, snapshot); err != nil {
		return
	}

	// Stream ticks in order, waiting for new ones as the match progresses.
	idx := 0
	for {
		select {
		case <-connDone:
			return
		default:
		}

		ticks, over, done := lm.nextFrom(idx)

		select {
		case <-connDone:
			return
		default:
		}

		for _, t := range ticks {
			if err := conn.WriteMessage(websocket.TextMessage, t); err != nil {
				return
			}
			idx++
		}

		if done {
			if over != nil {
				conn.WriteMessage(websocket.TextMessage, over)
			}
			return
		}
	}
}

// waitForLiveMatch polls until the match runner registers a liveMatch or timeout.
func (srv *server) waitForLiveMatch(matchID string, timeout time.Duration) *liveMatch {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		lm, ok := srv.liveMatches[matchID]
		srv.mu.RUnlock()
		if ok {
			return lm
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}
