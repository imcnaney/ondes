// Package audio provides WAV file I/O for the renderer. Mono, 16-bit
// PCM, 44.1 kHz - matching what regression/summarize.py expects.
package audio

import (
	"encoding/binary"
	"fmt"
	"os"
)

const (
	SampleRate    = 44100
	BitsPerSample = 16
	NumChannels   = 1
)

// WriteMono16 writes a mono 16-bit PCM WAV file at the given sample
// rate. Input is float64 samples in [-1, +1]; values outside that range
// are hard-clipped at the int16 boundary.
func WriteMono16(path string, samples []float64, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	byteRate := sampleRate * NumChannels * BitsPerSample / 8
	blockAlign := uint16(NumChannels * BitsPerSample / 8)
	dataSize := uint32(len(samples) * NumChannels * BitsPerSample / 8)

	// RIFF header
	if _, err := f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(36+dataSize)); err != nil {
		return err
	}
	if _, err := f.Write([]byte("WAVE")); err != nil {
		return err
	}

	// fmt chunk
	if _, err := f.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil { // PCM
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(NumChannels)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(byteRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, blockAlign); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(BitsPerSample)); err != nil {
		return err
	}

	// data chunk
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, dataSize); err != nil {
		return err
	}

	buf := make([]byte, 2*len(samples))
	for i, s := range samples {
		v := int32(s * 32767)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(v)))
	}
	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("wav write: %w", err)
	}
	return nil
}
