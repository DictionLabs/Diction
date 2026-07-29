package core

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWriteWAVHeader(t *testing.T) {
	const dataSize = 32000 // 1 second of 16kHz 16-bit mono

	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, dataSize); err != nil {
		t.Fatalf("WriteWAVHeader error: %v", err)
	}

	h := buf.Bytes()
	if len(h) != 44 {
		t.Fatalf("expected 44 bytes, got %d", len(h))
	}

	// RIFF chunk ID
	if string(h[0:4]) != "RIFF" {
		t.Errorf("expected RIFF, got %q", string(h[0:4]))
	}
	// File size = 36 + dataSize
	fileSize := binary.LittleEndian.Uint32(h[4:8])
	if fileSize != uint32(36+dataSize) {
		t.Errorf("file size: want %d, got %d", 36+dataSize, fileSize)
	}
	// WAVE format
	if string(h[8:12]) != "WAVE" {
		t.Errorf("expected WAVE, got %q", string(h[8:12]))
	}
	// fmt chunk
	if string(h[12:16]) != "fmt " {
		t.Errorf("expected 'fmt ', got %q", string(h[12:16]))
	}
	// fmt chunk size = 16
	if binary.LittleEndian.Uint32(h[16:20]) != 16 {
		t.Errorf("fmt chunk size: want 16, got %d", binary.LittleEndian.Uint32(h[16:20]))
	}
	// PCM format = 1
	if binary.LittleEndian.Uint16(h[20:22]) != 1 {
		t.Errorf("audio format: want 1 (PCM), got %d", binary.LittleEndian.Uint16(h[20:22]))
	}
	// Channels = 1 (mono)
	if binary.LittleEndian.Uint16(h[22:24]) != 1 {
		t.Errorf("channels: want 1, got %d", binary.LittleEndian.Uint16(h[22:24]))
	}
	// Sample rate = 16000
	if binary.LittleEndian.Uint32(h[24:28]) != 16000 {
		t.Errorf("sample rate: want 16000, got %d", binary.LittleEndian.Uint32(h[24:28]))
	}
	// Byte rate = 16000 * 1 * 16/8 = 32000
	if binary.LittleEndian.Uint32(h[28:32]) != 32000 {
		t.Errorf("byte rate: want 32000, got %d", binary.LittleEndian.Uint32(h[28:32]))
	}
	// Block align = 1 * 16/8 = 2
	if binary.LittleEndian.Uint16(h[32:34]) != 2 {
		t.Errorf("block align: want 2, got %d", binary.LittleEndian.Uint16(h[32:34]))
	}
	// Bits per sample = 16
	if binary.LittleEndian.Uint16(h[34:36]) != 16 {
		t.Errorf("bits per sample: want 16, got %d", binary.LittleEndian.Uint16(h[34:36]))
	}
	// data chunk ID
	if string(h[36:40]) != "data" {
		t.Errorf("expected 'data', got %q", string(h[36:40]))
	}
	// data chunk size
	if binary.LittleEndian.Uint32(h[40:44]) != uint32(dataSize) {
		t.Errorf("data size: want %d, got %d", dataSize, binary.LittleEndian.Uint32(h[40:44]))
	}
}

func TestParseWAVDurationMs(t *testing.T) {
	// 1 second at 16kHz 16-bit mono = 32000 bytes
	const dataSize = 32000
	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, dataSize); err != nil {
		t.Fatal(err)
	}
	buf.Write(make([]byte, dataSize))
	got := ParseWAVDurationMs(buf.Bytes())
	if got != 1000 {
		t.Errorf("ParseWAVDurationMs: want 1000ms, got %d", got)
	}

	// 2.5 seconds
	var buf2 bytes.Buffer
	dataSize2 := 32000 * 5 / 2
	if err := WriteWAVHeader(&buf2, dataSize2); err != nil {
		t.Fatal(err)
	}
	buf2.Write(make([]byte, dataSize2))
	got2 := ParseWAVDurationMs(buf2.Bytes())
	if got2 != 2500 {
		t.Errorf("ParseWAVDurationMs 2.5s: want 2500ms, got %d", got2)
	}

	// Non-WAV returns 0
	if got3 := ParseWAVDurationMs([]byte("not a wav file")); got3 != 0 {
		t.Errorf("non-WAV: want 0, got %d", got3)
	}
}

func TestWriteWAVHeader_ZeroData(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(buf.Bytes()) != 44 {
		t.Errorf("expected 44 bytes even for zero data size")
	}
}
