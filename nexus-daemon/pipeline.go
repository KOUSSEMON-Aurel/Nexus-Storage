package main

import (
	"fmt"
)

// FrameHandler is a function that processes a single PNG frame (e.g., uploads it).
type FrameHandler func(frame []byte) error

// StreamingNexusWriter implements io.WriteCloser and pipes data through
// the Rust-powered encryption and encoding pipeline.
type StreamingNexusWriter struct {
	crypto       *CryptoStream
	encoder      *EncodeStream
	frameHandler FrameHandler
	closed       bool
}

// NewStreamingNexusWriter initializes a stateful pipeline.
func (nc *NexusCore) NewStreamingNexusWriter(key []byte, mode int, handler FrameHandler) (*StreamingNexusWriter, error) {
	crypto, err := nc.InitEncryptStream(key)
	if err != nil {
		return nil, err
	}

	encoder, err := nc.InitEncodeStream(mode)
	if err != nil {
		crypto.Close()
		return nil, err
	}

	// 3. Inject the crypto header (NoncePrefix) as the first chunk
	if _, err := encoder.Push(crypto.NoncePrefix); err != nil {
		crypto.Close()
		encoder.Close()
		return nil, fmt.Errorf("failed to inject pipeline header: %w", err)
	}

	return &StreamingNexusWriter{
		crypto:       crypto,
		encoder:      encoder,
		frameHandler: handler,
	}, nil
}

// Write encrypts and encodes the provided bytes.
// It returns the number of bytes processed and any error.
func (sw *StreamingNexusWriter) Write(p []byte) (int, error) {
	if sw.closed {
		return 0, fmt.Errorf("writer is closed")
	}

	// 1. Encrypt the chunk
	ciphertext, err := sw.crypto.EncryptUpdate(p)
	if err != nil {
		return 0, fmt.Errorf("pipeline encryption failed: %w", err)
	}

	// 2. Push ciphertext to the encoder
	_, err = sw.encoder.Push(ciphertext)
	if err != nil {
		return 0, fmt.Errorf("pipeline encoding failed: %w", err)
	}

	// 3. Pop and handle all ready frames
	for {
		frame, err := sw.encoder.PopFrame()
		if err != nil {
			return 0, fmt.Errorf("failed to extract frame from pipeline: %w", err)
		}
		if frame == nil {
			break
		}

		if err := sw.frameHandler(frame); err != nil {
			return 0, fmt.Errorf("frame handler failed: %w", err)
		}
	}

	return len(p), nil
}

// Close finalizes the encryption and encoding, flushes remaining data,
// and cleans up native resources.
func (sw *StreamingNexusWriter) Close() error {
	if sw.closed {
		return nil
	}
	sw.closed = true

	// Ensure we close native contexts at the end
	defer sw.crypto.Close()
	defer sw.encoder.Close()

	// 1. Finalize encryption (with empty final chunk if needed, or we could have
	// required the last chunk here, but io.WriteCloser standard usually wants Close() to flush).
	// For STREAM, we'll finalize with empty if no data was provided in the last write.
	lastCiphertext, err := sw.crypto.EncryptFinalize([]byte{})
	if err != nil {
		return fmt.Errorf("failed to finalize encryption stream: %w", err)
	}

	// 2. Push final ciphertext
	if len(lastCiphertext) > 0 {
		_, err = sw.encoder.Push(lastCiphertext)
		if err != nil {
			return fmt.Errorf("failed to push final ciphertext: %w", err)
		}
	}

	// 3. Finalize encoder (flush partial buffer)
	if err := sw.encoder.Finalize(); err != nil {
		return fmt.Errorf("failed to finalize encoder stream: %w", err)
	}

	// 4. Pop final frames
	for {
		frame, err := sw.encoder.PopFrame()
		if err != nil {
			return fmt.Errorf("failed to extract final frames: %w", err)
		}
		if frame == nil {
			break
		}

		if err := sw.frameHandler(frame); err != nil {
			return fmt.Errorf("final frame handler failed: %w", err)
		}
	}

	return nil
}
