package bgm

import "testing"

func TestPCMCodec(t *testing.T) {
	tests := []struct {
		bits   uint16
		codec  string
		filter string
	}{
		{8, "pcm_u8", "s16"},
		{16, "pcm_s16le", "s16"},
		{24, "pcm_s24le", "s32"},
		{32, "pcm_s32le", "s32"},
	}
	for _, tt := range tests {
		codec, filter, err := pcmCodec(tt.bits)
		if err != nil {
			t.Fatalf("pcmCodec(%d) error = %v", tt.bits, err)
		}
		if codec != tt.codec || filter != tt.filter {
			t.Fatalf("pcmCodec(%d) = %q/%q, want %q/%q", tt.bits, codec, filter, tt.codec, tt.filter)
		}
	}
	if _, _, err := pcmCodec(12); err == nil {
		t.Fatal("pcmCodec(12) accepted an unsupported bit depth")
	}
}
