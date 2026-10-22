package wavutil

import (
	"bytes"
	"testing"
	"time"
)

func testFormat(bits uint16, channels uint16) Format {
	blockAlign := channels * bits / 8
	return Format{
		AudioFormat:   1,
		Channels:      channels,
		SampleRate:    44100,
		ByteRate:      44100 * uint32(blockAlign),
		BlockAlign:    blockAlign,
		BitsPerSample: bits,
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	format := testFormat(16, 1)
	pcm := []byte{1, 2, 3, 4, 5, 6}
	encoded := Encode(format, pcm)

	wav, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !bytes.Equal(wav.Data, pcm) {
		t.Fatalf("data = %v, want %v", wav.Data, pcm)
	}
	if !wav.Format.Compatible(format) {
		t.Fatalf("format = %+v, want compatible with %+v", wav.Format, format)
	}
}

func TestParseAcceptsOddDataWithPad(t *testing.T) {
	format := testFormat(8, 1)
	encoded := Encode(format, []byte{1, 2, 3})
	wav, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(wav.Data) != 3 {
		t.Fatalf("data length = %d, want 3", len(wav.Data))
	}
}

func TestParseRejectsTruncatedAndNonPCM(t *testing.T) {
	format := testFormat(16, 1)
	encoded := Encode(format, []byte{1, 2, 3, 4})

	if _, err := Parse(encoded[:len(encoded)-1]); err == nil {
		t.Fatal("Parse() accepted truncated data")
	}
	if _, err := Parse(append(encoded, 0)); err == nil {
		t.Fatal("Parse() accepted trailing bytes")
	}

	nonPCM := format
	nonPCM.AudioFormat = 3
	if _, err := Parse(Encode(nonPCM, []byte{1, 2})); err == nil {
		t.Fatal("Parse() accepted a non-PCM format")
	}
}

func TestSilence(t *testing.T) {
	format := testFormat(16, 1)
	silence := Silence(format, 200*time.Millisecond)
	if want := 44100 * 200 / 1000 * 2; len(silence) != want {
		t.Fatalf("silence length = %d, want %d", len(silence), want)
	}
	for i, b := range silence {
		if b != 0 {
			t.Fatalf("silence[%d] = %d, want 0", i, b)
		}
	}

	format.BitsPerSample = 8
	format.BlockAlign = 1
	format.ByteRate = 44100
	silence = Silence(format, 10*time.Millisecond)
	if len(silence) == 0 || silence[0] != 0x80 {
		t.Fatalf("8-bit silence = %v, want 0x80 fill", silence)
	}
	if Silence(format, 0) != nil {
		t.Fatal("Silence(0) should be empty")
	}
}
