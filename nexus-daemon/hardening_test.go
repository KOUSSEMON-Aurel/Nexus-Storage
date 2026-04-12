package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"
)

func TestHardening_Adversarial(t *testing.T) {
	nc := &NexusCore{}
	key := make([]byte, 32)
	rand.Read(key)

	// --- Helper: Encrypt data and get frames ---
	getFrames := func(data []byte) [][]byte {
		var frames [][]byte
		writer, _ := nc.NewStreamingNexusWriter(key, 0, func(f []byte) error {
			frames = append(frames, f)
			return nil
		})
		writer.Write(data)
		writer.Close()
		return frames
	}

	// --- Helper: Decrypt frames and expect error or success ---
	decryptFrames := func(frames [][]byte) ([]byte, error) {
		if len(frames) == 0 {
			return nil, fmt.Errorf("no frames")
		}

		decoder, _ := nc.InitDecodeStream(0)
		defer decoder.Close()

		var chunks [][]byte
		for _, f := range frames {
			if err := decoder.Push(f); err != nil {
				return nil, err
			}
			for {
				c, _ := decoder.Pop()
				if c == nil {
					break
				}
				chunks = append(chunks, c)
			}
		}

		if len(chunks) == 0 {
			return nil, fmt.Errorf("no chunks decoded")
		}

		var decrypted []byte
		var crypto *CryptoStream
		defer func() {
			if crypto != nil {
				crypto.Close()
			}
		}()

		for i, chunk := range chunks {
			if crypto == nil {
				if len(chunk) < 16 {
					continue
				}
				var err error
				crypto, err = nc.InitDecryptStream(key, chunk[:16])
				if err != nil {
					return nil, err
				}
				chunk = chunk[16:]
				if len(chunk) == 0 {
					continue
				}
			}

			p, err := crypto.DecryptUpdate(chunk)
			if err != nil {
				return nil, fmt.Errorf("decryption update error at chunk %d/%d: %w", i, len(chunks), err)
			}
			decrypted = append(decrypted, p...)
		}

		if crypto != nil {
			p, err := crypto.DecryptFinalize([]byte{})
			if err != nil {
				return nil, fmt.Errorf("decryption finalize error: %w", err)
			}
			decrypted = append(decrypted, p...)
		}
		return decrypted, nil
	}

	payload := make([]byte, 100*1024) // 100KB
	rand.Read(payload)

	t.Run("Roundtrip", func(t *testing.T) {
		frames := getFrames(payload)
		dec, err := decryptFrames(frames)
		if err != nil {
			t.Fatalf("Normal roundtrip failed: %v", err)
		}
		if !bytes.Equal(payload, dec) {
			t.Error("Payload mismatch")
		}
	})

	t.Run("BitFlipCorruption", func(t *testing.T) {
		frames := getFrames(payload)
		// Corrupt a middle frame's pixel data
		frames[1][len(frames[1])/2] ^= 0x42
		_, err := decryptFrames(frames)
		if err == nil {
			t.Fatal("Bit-flip was NOT detected!")
		}
		t.Logf("Caught expected error: %v", err)
	})

	t.Run("ReorderingChunks", func(t *testing.T) {
		frames := getFrames(make([]byte, 3*1024*1024)) // 3MB to ensure multiple frames
		if len(frames) < 3 {
			t.Skip("Not enough frames generated for reordering test")
		}
		// Swap frame 1 and 2 (ignoring frame 0 which is header)
		frames[1], frames[2] = frames[2], frames[1]
		_, err := decryptFrames(frames)
		if err == nil {
			t.Fatal("Reordering was NOT detected!")
		}
		t.Logf("Caught expected error: %v", err)
	})

	t.Run("Truncation", func(t *testing.T) {
		frames := getFrames(payload)
		if len(frames) < 2 {
			t.Skip("Not enough frames for truncation test")
		}
		// Remove the last frame (which contains the finalize EOS signal)
		truncated := frames[:len(frames)-1]
		_, err := decryptFrames(truncated)
		if err == nil {
			t.Fatal("Truncation was NOT detected!")
		}
		t.Logf("Caught expected error: %v", err)
	})
}
