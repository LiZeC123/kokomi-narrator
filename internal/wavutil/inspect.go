package wavutil

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

// Info describes a PCM WAV file without loading its audio payload.
type Info struct {
	Format   Format
	DataSize int64
	Duration time.Duration
}

// InspectFile reads the RIFF/WAVE headers of path and computes its duration
// from the fmt and data chunk sizes. The data chunk itself is not read.
func InspectFile(path string) (*Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	size := stat.Size()
	if size < 12 {
		return nil, fmt.Errorf("file too short for RIFF/WAVE header")
	}

	var header [12]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, err
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}
	riffSize := int64(binary.LittleEndian.Uint32(header[4:8]))
	if riffSize < 4 {
		return nil, fmt.Errorf("invalid RIFF chunk size %d", riffSize)
	}
	if riffSize+8 != size {
		return nil, fmt.Errorf("incomplete RIFF/WAVE data (declared %d bytes, got %d)", riffSize+8, size)
	}

	var (
		format     Format
		formatSeen bool
		dataSize   int64
		dataSeen   bool
	)

	pos := int64(12)
	for pos+8 <= size {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(f, chunkHeader[:]); err != nil {
			return nil, err
		}
		chunkID := string(chunkHeader[0:4])
		chunkSize := int64(binary.LittleEndian.Uint32(chunkHeader[4:8]))
		payload := pos + 8
		if chunkSize < 0 || payload+chunkSize > size {
			return nil, fmt.Errorf("chunk %q is truncated", chunkID)
		}
		padded := chunkSize + chunkSize%2
		skip := padded

		switch chunkID {
		case "fmt ":
			if formatSeen {
				return nil, fmt.Errorf("duplicate fmt chunk")
			}
			if chunkSize < 16 {
				return nil, fmt.Errorf("fmt chunk is too short (%d bytes)", chunkSize)
			}
			if chunkSize > 1<<20 {
				return nil, fmt.Errorf("fmt chunk is too large (%d bytes)", chunkSize)
			}
			chunk := make([]byte, chunkSize)
			if _, err := io.ReadFull(f, chunk); err != nil {
				return nil, err
			}
			parsed, err := parseFormat(chunk)
			if err != nil {
				return nil, err
			}
			format = parsed
			formatSeen = true
			skip = chunkSize % 2
		case "data":
			if dataSeen {
				return nil, fmt.Errorf("duplicate data chunk")
			}
			dataSize = chunkSize
			dataSeen = true
		}

		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return nil, err
		}
		pos = payload + padded
	}

	if pos != size {
		return nil, fmt.Errorf("unexpected trailing bytes after RIFF chunks")
	}
	if !formatSeen {
		return nil, fmt.Errorf("missing fmt chunk")
	}
	if !dataSeen {
		return nil, fmt.Errorf("missing data chunk")
	}
	if format.BlockAlign == 0 || dataSize%int64(format.BlockAlign) != 0 {
		return nil, fmt.Errorf("data chunk is not a whole number of frames")
	}

	return &Info{
		Format:   format,
		DataSize: dataSize,
		Duration: time.Duration(float64(dataSize) / float64(format.ByteRate) * float64(time.Second)),
	}, nil
}
