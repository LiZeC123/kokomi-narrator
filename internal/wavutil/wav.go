package wavutil

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Format is the parsed content of a WAV fmt chunk.
type Format struct {
	AudioFormat   uint16
	Channels      uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
	Raw           []byte
}

// WAV is a parsed PCM RIFF/WAVE file.
type WAV struct {
	Format Format
	Data   []byte
}

func (f Format) Compatible(other Format) bool {
	return f.AudioFormat == other.AudioFormat &&
		f.Channels == other.Channels &&
		f.SampleRate == other.SampleRate &&
		f.ByteRate == other.ByteRate &&
		f.BlockAlign == other.BlockAlign &&
		f.BitsPerSample == other.BitsPerSample
}

// Parse validates a RIFF/WAVE byte slice and extracts its fmt and data chunks.
// Only PCM is accepted.
func Parse(data []byte) (*WAV, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("file too short for RIFF/WAVE header")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}

	riffSize := int64(binary.LittleEndian.Uint32(data[4:8]))
	if riffSize < 4 {
		return nil, fmt.Errorf("invalid RIFF chunk size %d", riffSize)
	}
	end := riffSize + 8
	if end != int64(len(data)) {
		return nil, fmt.Errorf("incomplete RIFF/WAVE data (declared %d bytes, got %d)", end, len(data))
	}

	var (
		format     Format
		formatSeen bool
		pcm        []byte
		dataSeen   bool
	)

	pos := 12
	for pos+8 <= len(data) {
		chunkID := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		payload := pos + 8
		if payload+size > len(data) {
			return nil, fmt.Errorf("chunk %q is truncated", chunkID)
		}
		chunk := data[payload : payload+size]

		switch chunkID {
		case "fmt ":
			if formatSeen {
				return nil, fmt.Errorf("duplicate fmt chunk")
			}
			if size < 16 {
				return nil, fmt.Errorf("fmt chunk is too short (%d bytes)", size)
			}
			f, err := parseFormat(chunk)
			if err != nil {
				return nil, err
			}
			format = f
			formatSeen = true
		case "data":
			if dataSeen {
				return nil, fmt.Errorf("duplicate data chunk")
			}
			pcm = chunk
			dataSeen = true
		}

		pos = payload + size
		if size%2 == 1 {
			pos++
		}
	}

	if !formatSeen {
		return nil, fmt.Errorf("missing fmt chunk")
	}
	if !dataSeen {
		return nil, fmt.Errorf("missing data chunk")
	}
	if format.AudioFormat != 1 {
		return nil, fmt.Errorf("unsupported audio format %d (only PCM is supported)", format.AudioFormat)
	}
	if len(pcm)%int(format.BlockAlign) != 0 {
		return nil, fmt.Errorf("data chunk is not a whole number of frames")
	}

	return &WAV{Format: format, Data: pcm}, nil
}

func parseFormat(chunk []byte) (Format, error) {
	f := Format{
		AudioFormat:   binary.LittleEndian.Uint16(chunk[0:2]),
		Channels:      binary.LittleEndian.Uint16(chunk[2:4]),
		SampleRate:    binary.LittleEndian.Uint32(chunk[4:8]),
		ByteRate:      binary.LittleEndian.Uint32(chunk[8:12]),
		BlockAlign:    binary.LittleEndian.Uint16(chunk[12:14]),
		BitsPerSample: binary.LittleEndian.Uint16(chunk[14:16]),
		Raw:           append([]byte(nil), chunk...),
	}

	if f.AudioFormat != 1 {
		return Format{}, fmt.Errorf("unsupported audio format %d (only PCM is supported)", f.AudioFormat)
	}
	if f.Channels == 0 {
		return Format{}, fmt.Errorf("fmt chunk has zero channels")
	}
	if f.SampleRate == 0 {
		return Format{}, fmt.Errorf("fmt chunk has zero sample rate")
	}
	switch f.BitsPerSample {
	case 8, 16, 24, 32:
	default:
		return Format{}, fmt.Errorf("unsupported PCM bit depth %d", f.BitsPerSample)
	}
	wantBlockAlign := uint32(f.Channels) * uint32(f.BitsPerSample) / 8
	if uint32(f.BlockAlign) != wantBlockAlign {
		return Format{}, fmt.Errorf("invalid blockAlign %d (want %d)", f.BlockAlign, wantBlockAlign)
	}
	wantByteRate := f.SampleRate * uint32(f.BlockAlign)
	if f.ByteRate != wantByteRate {
		return Format{}, fmt.Errorf("invalid byteRate %d (want %d)", f.ByteRate, wantByteRate)
	}
	return f, nil
}

func ReadFile(path string) (*WAV, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	w, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return w, nil
}

// IsValidPCMFile reports whether path exists and contains a complete PCM WAV.
func IsValidPCMFile(path string) bool {
	_, err := ReadFile(path)
	return err == nil
}

// WriteAtomic writes a validated WAV via <path>.tmp and an atomic rename.
func WriteAtomic(path string, data []byte) error {
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("refusing to write invalid WAV: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if _, err := ReadFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("validation of %s failed: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Encode builds a RIFF/WAVE file using format's fmt chunk and the supplied PCM data.
func Encode(format Format, pcm []byte) []byte {
	fmtRaw := format.Raw
	if len(fmtRaw) < 16 {
		fmtRaw = encodeFormat(format)
	}

	fmtPad := len(fmtRaw) % 2
	dataPad := len(pcm) % 2
	riffSize := 4 + 8 + len(fmtRaw) + fmtPad + 8 + len(pcm) + dataPad

	var buf bytes.Buffer
	buf.Grow(8 + riffSize)

	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(riffSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(fmtRaw)))
	buf.Write(fmtRaw)
	if fmtPad == 1 {
		buf.WriteByte(0)
	}
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	if dataPad == 1 {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

func encodeFormat(f Format) []byte {
	raw := make([]byte, 16)
	binary.LittleEndian.PutUint16(raw[0:2], f.AudioFormat)
	binary.LittleEndian.PutUint16(raw[2:4], f.Channels)
	binary.LittleEndian.PutUint32(raw[4:8], f.SampleRate)
	binary.LittleEndian.PutUint32(raw[8:12], f.ByteRate)
	binary.LittleEndian.PutUint16(raw[12:14], f.BlockAlign)
	binary.LittleEndian.PutUint16(raw[14:16], f.BitsPerSample)
	return raw
}

// Silence returns PCM silence of the requested duration.
func Silence(format Format, d time.Duration) []byte {
	if d <= 0 {
		return nil
	}
	frames := int(float64(format.SampleRate) * d.Seconds())
	if frames <= 0 {
		return nil
	}
	size := frames * int(format.BlockAlign)
	if size <= 0 {
		return nil
	}
	fill := byte(0x00)
	if format.BitsPerSample == 8 {
		fill = 0x80
	}
	silence := make([]byte, size)
	for i := range silence {
		silence[i] = fill
	}
	return silence
}
