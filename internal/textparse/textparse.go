package textparse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Kind string

const (
	KindSpeech      Kind = "speech"
	KindPunctuation Kind = "punctuation"
)

// Segment is one non-empty line of a text file.
type Segment struct {
	Seq  int
	Text string
	Hash string
	Kind Kind
}

// Parse validates and splits a text file into segments.
func Parse(raw []byte) ([]Segment, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("file is not valid UTF-8")
	}
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var segments []Segment
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		segments = append(segments, Segment{
			Seq:  len(segments) + 1,
			Text: line,
			Hash: HashText(line),
			Kind: Classify(line),
		})
	}
	return segments, nil
}

// Classify reports whether a line contains something pronounceable.
func Classify(text string) Kind {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	for _, r := range t {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return KindSpeech
		}
	}
	return KindPunctuation
}

func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
