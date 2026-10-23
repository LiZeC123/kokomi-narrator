package wavutil

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempWAV(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.wav")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInspectFile(t *testing.T) {
	format := testFormat(16, 1)
	data := make([]byte, format.ByteRate) // one second
	path := writeTempWAV(t, Encode(format, data))

	info, err := InspectFile(path)
	if err != nil {
		t.Fatalf("InspectFile() error = %v", err)
	}
	if info.DataSize != int64(len(data)) {
		t.Fatalf("DataSize = %d, want %d", info.DataSize, len(data))
	}
	if info.Duration != time.Second {
		t.Fatalf("Duration = %v, want 1s", info.Duration)
	}
	if !info.Format.Compatible(format) {
		t.Fatalf("Format = %+v, want compatible with %+v", info.Format, format)
	}
}

func TestInspectFileRejectsInvalidWAV(t *testing.T) {
	format := testFormat(16, 1)
	valid := Encode(format, []byte{1, 2, 3, 4})

	if _, err := InspectFile(writeTempWAV(t, valid[:len(valid)-1])); err == nil {
		t.Fatal("InspectFile() accepted truncated data")
	}
	if _, err := InspectFile(writeTempWAV(t, append(append([]byte{}, valid...), 0))); err == nil {
		t.Fatal("InspectFile() accepted trailing bytes")
	}

	nonPCM := format
	nonPCM.AudioFormat = 3
	if _, err := InspectFile(writeTempWAV(t, Encode(nonPCM, []byte{1, 2}))); err == nil {
		t.Fatal("InspectFile() accepted a non-PCM format")
	}
}

func TestInspectFileSkipsUnknownChunks(t *testing.T) {
	format := testFormat(16, 1)
	pcm := []byte{1, 2, 3, 4}
	wav := Encode(format, pcm)

	// Insert an even-sized unknown chunk between fmt and data.
	fmtSize := len(format.Raw)
	if fmtSize < 16 {
		fmtSize = 16
	}
	dataOffset := 12 + 8 + fmtSize + fmtSize%2
	extra := make([]byte, 8+4)
	copy(extra[0:4], "LIST")
	binary.LittleEndian.PutUint32(extra[4:8], 4)
	copy(extra[8:], "test")

	withExtra := append([]byte{}, wav[:dataOffset]...)
	withExtra = append(withExtra, extra...)
	withExtra = append(withExtra, wav[dataOffset:]...)
	binary.LittleEndian.PutUint32(withExtra[4:8], uint32(len(withExtra)-8))

	path := writeTempWAV(t, withExtra)
	info, err := InspectFile(path)
	if err != nil {
		t.Fatalf("InspectFile() error = %v", err)
	}
	if info.DataSize != int64(len(pcm)) || info.Duration <= 0 {
		t.Fatalf("info = %+v, want data size %d and positive duration", info, len(pcm))
	}
}
