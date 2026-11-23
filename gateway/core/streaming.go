package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	streamTimeout   = 3 * time.Hour
	wsCloseUnknown  = 4000
	wsCloseDown     = 4001
	wsCloseFailed   = 4002
	wsCloseTooLarge = 4003
	wsCloseNoAudio  = 4004

	maxWSMessageBytes = 8 << 20
)

const errTypeSTTError = "stt_error"

const errTypeNoAudio = "no_audio"

const errTypeClientAbort = "client_abort"

const opusSubprotocol = "diction.opus.v1"

var (
	ffmpegLookMu   sync.Mutex
	ffmpegLookDone bool
	ffmpegBinPath  string

	opusSemOnce sync.Once
	opusSemCh   chan struct{}
)

func lookupFfmpeg() string {
	ffmpegLookMu.Lock()
	defer ffmpegLookMu.Unlock()
	if !ffmpegLookDone {
		p, err := exec.LookPath("ffmpeg")
		if err == nil {
			ffmpegBinPath = p
		}
		ffmpegLookDone = true
	}
	return ffmpegBinPath
}

func testSetFfmpegPath(path string) {
	ffmpegLookMu.Lock()
	defer ffmpegLookMu.Unlock()
	ffmpegBinPath = path
	ffmpegLookDone = true
}

func getOpusSem() chan struct{} {
	opusSemOnce.Do(func() {
		n := runtime.GOMAXPROCS(0)
		if s := os.Getenv("DICTION_OPUS_CONCURRENCY"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v > 0 {
				n = v
			}
		}
		opusSemCh = make(chan struct{}, n)
	})
	return opusSemCh
}

type audioPayload struct {
	data     []byte
	filename string
}

type limitedStderrWriter struct {
	buf bytes.Buffer
	cap int
}

func (lw *limitedStderrWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := lw.cap - lw.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		lw.buf.Write(p)
	}
	return n, nil
}

func readOggGranulepos(data []byte) int64 {
	for i := len(data) - 27; i >= 0; i-- {
		if data[i] != 'O' || data[i+1] != 'g' || data[i+2] != 'g' || data[i+3] != 'S' {
			continue
		}
		if data[i+4] != 0 {
			continue
		}
		if i+14 > len(data) {
			continue
		}
		raw := binary.LittleEndian.Uint64(data[i+6 : i+14])
		if raw == math.MaxUint64 {
			continue
		}
		if raw > math.MaxInt64 {
			return -1
		}
		return int64(raw)
	}
	return -1
}

func readOggPreSkip(data []byte) uint16 {
	if len(data) < 28 {
		return 0
	}
	if !bytes.Equal(data[0:4], []byte("OggS")) || data[4] != 0 {
		return 0
	}
	nseg := int(data[26])
	headerLen := 27 + nseg
	if headerLen > len(data) {
		return 0
	}
	head := data[headerLen:]
	if len(head) < 12 || !bytes.Equal(head[0:8], []byte("OpusHead")) {
		return 0
	}
	return binary.LittleEndian.Uint16(head[10:12])
}

func oggDurationMs(data []byte) int64 {
	gp := readOggGranulepos(data)
	if gp < 0 {
		return 0
	}
	preSkip := int64(readOggPreSkip(data))
	adjusted := gp - preSkip
	if adjusted <= 0 {
		return 0
	}
	return adjusted / 48
}

type streamAction struct {
	Action   string `json:"action"`
	Language string `json:"language,omitempty"`
	Model    string `json:"model,omitempty"`
	Text     string `json:"text,omitempty"`
}

type streamResult struct {
	Text     string `json:"text"`
	Mode     string `json:"mode,omitempty"`
	Status   string `json:"status,omitempty"`
	AutoEdit bool   `json:"auto_edit,omitempty"`
}

type intentFrame struct {
	Type   string `json:"type"`
	Intent string `json:"intent"`
	Source string `json:"source"`
}

const (
	wsReasonEOF           = "eof"
	wsReasonGoingAway     = "going_away"
	wsReasonIdleTimeout   = "idle_timeout"
	wsReasonStreamTimeout = "stream_timeout"
	wsReasonProtocol      = "protocol"
	wsReasonAbnormalClose = "abnormal_close"
	wsReasonUnknown       = "unknown"
)

func ClassifyWSError(err error) string {
	if err == nil {
		return ""
	}
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case websocket.StatusGoingAway:
			return wsReasonGoingAway
		case websocket.StatusProtocolError, websocket.StatusInvalidFramePayloadData,
			websocket.StatusUnsupportedData, websocket.StatusMandatoryExtension:
			return wsReasonProtocol
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return wsReasonStreamTimeout
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return wsReasonEOF
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "StatusAbnormalClosure"):
		return wsReasonAbnormalClose
	case strings.Contains(s, "EOF"):
		return wsReasonEOF
	case strings.Contains(s, "StatusGoingAway"), strings.Contains(s, "going away"):
		return wsReasonGoingAway
	case strings.Contains(s, "protocol"):
		return wsReasonProtocol
	}
	return wsReasonUnknown
}

func CloseWSWithTimeout(conn *websocket.Conn, code websocket.StatusCode, reason string, budget time.Duration) {
	done := make(chan struct{})
	go func() {
		_ = conn.Close(code, reason)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(budget):
	}
}

func (g *Gateway) StreamingHandler() http.HandlerFunc {
	return g.StreamingHandlerWithPostProcess(nil)
}

func (g *Gateway) StreamingHandlerWithPostProcess(
	postProcess func(context.Context, string, string, string) (string, string, error),
) http.HandlerFunc {
	return g.StreamingHandlerWithSplitEnhance(postProcess, nil)
}

func (g *Gateway) StreamingHandlerWithSplitEnhance(
	inline, postDelivery func(context.Context, string, string, string) (string, string, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g.newStreamSession(w, r, inline, postDelivery).serve()
	}
}
