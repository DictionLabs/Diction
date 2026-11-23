package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultDecodeMaxSeconds     = 2 * 60 * 60
	defaultDecodeTimeoutSeconds = 120
	decodedPCMBytesPerSecond    = 16000 * 2
	decodedHeaderHeadroom       = 1 << 20
)

var errDecodeBusy = errors.New("audio decode: server busy")

var decodeQueueWait = 10 * time.Second

type decodeLimits struct {
	maxSeconds int
	maxOutput  int64
	timeout    time.Duration
	sem        chan struct{}
}

var (
	decodeLimitsMu  sync.Mutex
	decodeLimitsCur *decodeLimits
)

func currentDecodeLimits() *decodeLimits {
	decodeLimitsMu.Lock()
	defer decodeLimitsMu.Unlock()
	if decodeLimitsCur == nil {
		maxSec := positiveEnvInt("DICTION_DECODE_MAX_SECONDS", defaultDecodeMaxSeconds)
		conc := positiveEnvInt("DICTION_DECODE_CONCURRENCY", max(4, runtime.GOMAXPROCS(0)))
		decodeLimitsCur = &decodeLimits{
			maxSeconds: maxSec,
			maxOutput:  int64(maxSec)*decodedPCMBytesPerSecond + decodedHeaderHeadroom,
			timeout:    time.Duration(positiveEnvInt("DICTION_DECODE_TIMEOUT_SECONDS", defaultDecodeTimeoutSeconds)) * time.Second,
			sem:        make(chan struct{}, conc),
		}
	}
	return decodeLimitsCur
}

func resetDecodeLimits() {
	decodeLimitsMu.Lock()
	decodeLimitsCur = nil
	decodeLimitsMu.Unlock()
}

func positiveEnvInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}

func convertToWAVBytes(audioData []byte, filename string) ([]byte, error) {
	return convertToWAVBytesCtx(context.Background(), audioData, filename)
}

func convertToWAVBytesCtx(ctx context.Context, audioData []byte, filename string) ([]byte, error) {
	if len(audioData) >= 4 && string(audioData[:4]) == "RIFF" {
		return audioData, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lim := currentDecodeLimits()

	release, err := acquireConvertSlot(ctx, lim)
	if err != nil {
		return nil, err
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, lim.timeout)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "diction-convert-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	ext := ".m4a"
	if filename != "" {
		if e := filepath.Ext(filename); e != "" {
			ext = e
		}
	}

	return runFFmpegConvert(ctx, lim, tmpDir, ext, audioData)
}

func acquireConvertSlot(ctx context.Context, lim *decodeLimits) (func(), error) {
	wait := time.NewTimer(decodeQueueWait)
	defer wait.Stop()
	select {
	case lim.sem <- struct{}{}:
		return func() { <-lim.sem }, nil
	case <-wait.C:
		return nil, errDecodeBusy
	case <-ctx.Done():
		return nil, fmt.Errorf("audio decode: %w", ctx.Err())
	}
}

func runFFmpegConvert(ctx context.Context, lim *decodeLimits, tmpDir, ext string, audioData []byte) ([]byte, error) {
	outPath := filepath.Join(tmpDir, "output.wav")

	inPath, err := writeTempInput(tmpDir, ext, audioData)
	if err != nil {
		return nil, fmt.Errorf("write temp input: %w", err)
	}

	cmd := ffmpegCommand(ctx, "ffmpeg", "-nostdin", "-y", "-i", inPath,
		"-t", strconv.Itoa(lim.maxSeconds),
		"-ar", "16000", "-ac", "1", "-f", "wav",
		"-fs", strconv.FormatInt(lim.maxOutput, 10),
		outPath)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ffmpeg convert: %w", ctx.Err())
		}
		return nil, fmt.Errorf("ffmpeg convert: %w", err)
	}

	f, err := os.Open(filepath.Clean(outPath))
	if err != nil {
		return nil, fmt.Errorf("read wav output: %w", err)
	}
	defer func() { _ = f.Close() }()
	wavData, err := io.ReadAll(io.LimitReader(f, lim.maxOutput+1))
	if err != nil {
		return nil, fmt.Errorf("read wav output: %w", err)
	}
	if int64(len(wavData)) > lim.maxOutput {
		return nil, fmt.Errorf("ffmpeg convert: decoded output exceeds %d bytes", lim.maxOutput)
	}
	return wavData, nil
}

func writeTempInput(dir, ext string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, "input-*"+strings.ReplaceAll(ext, "*", "_"))
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return "", werr
	}
	return f.Name(), cerr
}
