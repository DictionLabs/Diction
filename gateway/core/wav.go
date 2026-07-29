package core

import (
	"encoding/binary"
	"io"
)

// Wire format of the PCM the gateway accepts on the streaming paths: 16 kHz,
// 16-bit, mono. Package-level so callers can convert a PCM byte count to a
// duration without restating the numbers — see pcmBytesPerSecond.
const (
	sampleRate    = 16000
	bitsPerSample = 16
	numChannels   = 1
	byteRate      = sampleRate * numChannels * bitsPerSample / 8
	blockAlign    = numChannels * bitsPerSample / 8

	// pcmBytesPerSecond converts an accumulated PCM buffer length to milliseconds:
	// durationMs = bytes * 1000 / pcmBytesPerSecond.
	pcmBytesPerSecond = byteRate
)

// WriteWAVHeader writes a 44-byte RIFF WAV header for 16kHz, 16-bit, mono PCM.
func WriteWAVHeader(w io.Writer, dataSize int) error {
	fileSize := uint32(36 + dataSize)

	var header [44]byte
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], fileSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(header[22:24], numChannels)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], byteRate)
	binary.LittleEndian.PutUint16(header[32:34], blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))

	_, err := w.Write(header[:])
	return err
}

// ParseWAVDurationMs returns the duration of a WAV file in milliseconds.
// Scans for the "data" subchunk to handle files with extra chunks (e.g. LIST).
// Returns 0 if data is not valid WAV or duration cannot be determined.
func ParseWAVDurationMs(data []byte) int64 {
	if len(data) < 44 {
		return 0
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0
	}
	sampleRate := int64(binary.LittleEndian.Uint32(data[24:28]))
	channels := int64(binary.LittleEndian.Uint16(data[22:24]))
	bitsPerSample := int64(binary.LittleEndian.Uint16(data[34:36]))
	byteRate := sampleRate * channels * bitsPerSample / 8
	if byteRate == 0 {
		return 0
	}
	// Scan subchunks starting after the fmt chunk (byte 36 in standard WAV).
	i := 12
	for i+8 <= len(data) {
		chunkID := string(data[i : i+4])
		chunkSize := int64(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		if chunkID == "data" {
			return chunkSize * 1000 / byteRate
		}
		i += 8 + int(chunkSize)
		if chunkSize%2 != 0 {
			i++ // WAV chunks are padded to 2-byte boundary
		}
	}
	return 0
}
