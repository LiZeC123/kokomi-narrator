package textparse

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseNormalizesAndClassifies(t *testing.T) {
	raw := []byte("\ufeff第一行\r\n\r\n  Hello 123 \r，。！？\n🎉\n。。。\n")
	segments, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(segments) != 5 {
		t.Fatalf("Parse() returned %d segments, want 5", len(segments))
	}

	wantText := []string{"第一行", "Hello 123", "，。！？", "🎉", "。。。"}
	wantKind := []Kind{KindSpeech, KindSpeech, KindPunctuation, KindPunctuation, KindPunctuation}
	for i, seg := range segments {
		if seg.Seq != i+1 {
			t.Errorf("segment %d seq = %d, want %d", i, seg.Seq, i+1)
		}
		if seg.Text != wantText[i] {
			t.Errorf("segment %d text = %q, want %q", i, seg.Text, wantText[i])
		}
		if seg.Kind != wantKind[i] {
			t.Errorf("segment %d kind = %q, want %q", i, seg.Kind, wantKind[i])
		}
		if utf8.RuneCountInString(seg.Hash) != 64 {
			t.Errorf("segment %d hash length = %d, want 64", i, len(seg.Hash))
		}
		if seg.Hash != HashText(seg.Text) {
			t.Errorf("segment %d hash mismatch", i)
		}
	}
}

func TestParseRejectsInvalidUTF8(t *testing.T) {
	if _, err := Parse([]byte{0xff, 0xfe, 0x00}); err == nil {
		t.Fatal("Parse() accepted invalid UTF-8")
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		text string
		want Kind
	}{
		{"中文", KindSpeech},
		{"123", KindSpeech},
		{"  A  ", KindSpeech},
		{"，，", KindPunctuation},
		{"🎉🎉", KindPunctuation},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Classify(tt.text); got != tt.want {
			t.Errorf("Classify(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestHashBytesUsesRawBytes(t *testing.T) {
	a := HashBytes([]byte("a"))
	b := HashBytes([]byte("b"))
	if a == b || !strings.EqualFold(a, a) {
		t.Fatal("HashBytes returned an invalid result")
	}
}
