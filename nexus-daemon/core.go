package main

/*
#include <stdlib.h>
#include <stdint.h>
#include "nexus_core.h"

#cgo LDFLAGS: ${SRCDIR}/libnexus_core.a -lm -lpthread
#cgo linux LDFLAGS: -ldl
#cgo windows LDFLAGS: -lws2_32 -luserenv -lbcrypt -lntdll
*/
import "C"
import (
	"crypto/rand"
	"errors"
	"fmt"
	"unsafe"
)

// NexusCore is a wrapper around the C-FFI of nexus-core.
type NexusCore struct{}

func (nc *NexusCore) Encrypt(data []byte, password string) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}

	cPwd := C.CString(password)
	defer C.free(unsafe.Pointer(cPwd))

	var outPtr *C.uint8_t
	var outLen C.size_t

	res := C.nexus_encrypt(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		cPwd,
		&outPtr,
		&outLen,
	)

	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("encryption error (code %d)", res)
	}

	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (nc *NexusCore) Decrypt(data []byte, password string) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}

	cPwd := C.CString(password)
	defer C.free(unsafe.Pointer(cPwd))

	var outPtr *C.uint8_t
	var outLen C.size_t

	res := C.nexus_decrypt(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		cPwd,
		&outPtr,
		&outLen,
	)

	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("decryption error (code %d)", res)
	}

	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (nc *NexusCore) Compress(data []byte, level int) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}

	var outPtr *C.uint8_t
	var outLen C.size_t

	res := C.nexus_compress(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		C.int32_t(level),
		&outPtr,
		&outLen,
	)

	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("compression error (code %d)", res)
	}

	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (nc *NexusCore) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}

	var outPtr *C.uint8_t
	var outLen C.size_t

	res := C.nexus_decompress(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		&outPtr,
		&outLen,
	)

	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("decompression error (code %d)", res)
	}

	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (nc *NexusCore) Sha256(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty data")
	}

	outHex := make([]byte, 65)
	res := C.nexus_sha256_hex(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		(*C.char)(unsafe.Pointer(&outHex[0])),
	)

	if res != C.NEXUS_OK {
		return "", fmt.Errorf("hashing error (code %d)", res)
	}

	// Remove trailing null byte
	return string(outHex[:64]), nil
}

func (nc *NexusCore) EncodeToFrames(data []byte, outputDir string, mode int) (int, error) {
	if len(data) == 0 {
		return 0, errors.New("empty data")
	}

	cDir := C.CString(outputDir)
	defer C.free(unsafe.Pointer(cDir))

	res := C.nexus_encode_to_frames(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		cDir,
		C.int32_t(mode),
	)

	if res < 0 {
		return 0, fmt.Errorf("encoding error (code %d)", res)
	}

	return int(res), nil
}

func (nc *NexusCore) DecodeFromFrames(outputDir string, mode int) ([]byte, error) {
	cDir := C.CString(outputDir)
	defer C.free(unsafe.Pointer(cDir))

	var outPtr *C.uint8_t
	var outLen C.size_t

	res := C.nexus_decode_from_frames(
		cDir,
		C.int32_t(mode),
		&outPtr,
		&outLen,
	)

	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("decoding error (code %d)", res)
	}

	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

// ─── Per-File Raw-Key Wrappers ────────────────────────────────────────────────

// GenerateFileKey creates a cryptographically random 32-byte key for a file.
func (nc *NexusCore) GenerateFileKey() ([]byte, error) {
	var outPtr *C.uint8_t
	var outLen C.size_t
	res := C.nexus_generate_file_key(&outPtr, &outLen)
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("key generation error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

// EncryptWithKey encrypts `data` using a raw 32-byte key (no PBKDF2 step).
func (nc *NexusCore) EncryptWithKey(data, key []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	var outPtr *C.uint8_t
	var outLen C.size_t
	res := C.nexus_encrypt_with_key(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		&outPtr,
		&outLen,
	)
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("encrypt_with_key error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

// DecryptWithKey decrypts a blob produced by EncryptWithKey.
func (nc *NexusCore) DecryptWithKey(data, key []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	var outPtr *C.uint8_t
	var outLen C.size_t
	res := C.nexus_decrypt_with_key(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		&outPtr,
		&outLen,
	)
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("decrypt_with_key error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

// ─── Key Derivation (V4 Security) ─────────────────────────────────────────────

// GenerateRecoverySalt creates a cryptographically random 16-byte salt for Argon2.
func (nc *NexusCore) GenerateRecoverySalt() ([]byte, error) {
	var outPtr *C.uint8_t
	var outLen C.size_t
	res := C.nexus_generate_recovery_salt(&outPtr, &outLen)
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("salt generation error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

// DeriveMasterKey derives a 32-byte master key from password + salt using Argon2id.
// CRITICAL: Call this CLIENT-SIDE ONLY (in GUI or CLI).
// Never send password over network. Only send the result masterKey via IPC/secure channel.
func (nc *NexusCore) DeriveMasterKey(password string, salt []byte) ([]byte, error) {
	if len(salt) != 16 {
		return nil, fmt.Errorf("invalid salt length: expected 16, got %d", len(salt))
	}

	cPassword := C.CString(password)
	defer C.free(unsafe.Pointer(cPassword))

	var outPtr *C.uint8_t
	var outLen C.size_t

	res := C.nexus_derive_master_key(
		cPassword,
		C.size_t(len(password)),
		(*C.uint8_t)(unsafe.Pointer(&salt[0])),
		C.size_t(len(salt)),
		&outPtr,
		&outLen,
	)

	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("key derivation error (code %d)", res)
	}

	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

// ─── Streaming API (Hardened) ────────────────────────────────────────────────

// CryptoStream handles stateful encryption or decryption.
type CryptoStream struct {
	ctx         *C.StreamingContext
	NoncePrefix []byte
}

func (nc *NexusCore) InitEncryptStream(key []byte) (*CryptoStream, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid key length: %d", len(key))
	}
	prefix := make([]byte, 16)
	if _, err := rand.Read(prefix); err != nil {
		return nil, fmt.Errorf("failed to generate random prefix: %w", err)
	}
	ctx := C.nexus_encrypt_stream_init(
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		(*C.uint8_t)(unsafe.Pointer(&prefix[0])),
	)
	if ctx == nil {
		return nil, errors.New("failed to initialize encryption stream")
	}
	return &CryptoStream{ctx: ctx, NoncePrefix: prefix}, nil
}

func (nc *NexusCore) InitDecryptStream(key []byte, noncePrefix []byte) (*CryptoStream, error) {
	if len(key) != 32 || len(noncePrefix) != 16 {
		return nil, errors.New("invalid key or nonce prefix length")
	}
	ctx := C.nexus_decrypt_stream_init(
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		(*C.uint8_t)(unsafe.Pointer(&noncePrefix[0])),
	)
	if ctx == nil {
		return nil, errors.New("failed to initialize decryption stream")
	}
	return &CryptoStream{ctx: ctx, NoncePrefix: noncePrefix}, nil
}

func (s *CryptoStream) EncryptUpdate(data []byte) ([]byte, error) {
	var outPtr *C.uint8_t
	var outLen C.size_t
	var inPtr *C.uint8_t
	if len(data) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	res := C.nexus_encrypt_stream_update(s.ctx, inPtr, C.size_t(len(data)), &outPtr, &outLen)
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("encryption update error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (s *CryptoStream) DecryptUpdate(data []byte) ([]byte, error) {
	var outPtr *C.uint8_t
	var outLen C.size_t
	var inPtr *C.uint8_t
	if len(data) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	res := C.nexus_decrypt_stream_update(s.ctx, inPtr, C.size_t(len(data)), &outPtr, &outLen)
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("decryption update error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (s *CryptoStream) EncryptFinalize(data []byte) ([]byte, error) {
	var outPtr *C.uint8_t
	var outLen C.size_t
	var inPtr *C.uint8_t
	if len(data) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	res := C.nexus_encrypt_stream_finalize(s.ctx, inPtr, C.size_t(len(data)), &outPtr, &outLen)
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("encryption finalize error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (s *CryptoStream) DecryptFinalize(data []byte) ([]byte, error) {
	var outPtr *C.uint8_t
	var outLen C.size_t
	var inPtr *C.uint8_t
	if len(data) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	res := C.nexus_decrypt_stream_finalize(s.ctx, inPtr, C.size_t(len(data)), &outPtr, &outLen)
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("decryption finalize error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (s *CryptoStream) Close() {
	if s.ctx != nil {
		C.nexus_crypto_stream_drop(s.ctx)
		s.ctx = nil
	}
}

// EncodeStream handles stateful PNG frame generation.
type EncodeStream struct {
	ctx *C.StreamingEncoder
}

func (nc *NexusCore) InitEncodeStream(mode int) (*EncodeStream, error) {
	ctx := C.nexus_encode_stream_init(C.int32_t(mode))
	if ctx == nil {
		return nil, errors.New("failed to initialize encode stream")
	}
	return &EncodeStream{ctx: ctx}, nil
}

func (s *EncodeStream) Push(data []byte) (int, error) {
	var inPtr *C.uint8_t
	if len(data) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	res := C.nexus_encode_stream_push(s.ctx, inPtr, C.size_t(len(data)))
	if res < 0 {
		return 0, fmt.Errorf("encode push error (code %d)", res)
	}
	return int(res), nil
}

func (s *EncodeStream) PopFrame() ([]byte, error) {
	var outPtr *C.uint8_t
	var outLen C.size_t
	res := C.nexus_encode_stream_pop_frame(s.ctx, &outPtr, &outLen)
	if res == 1 {
		return nil, nil // No frames ready
	}
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("encode pop error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (s *EncodeStream) Finalize() error {
	res := C.nexus_encode_stream_finalize(s.ctx)
	if res != C.NEXUS_OK {
		return fmt.Errorf("encode finalize error (code %d)", res)
	}
	return nil
}

func (s *EncodeStream) Close() {
	if s.ctx != nil {
		C.nexus_encoder_stream_drop(s.ctx)
		s.ctx = nil
	}
}

// DecodeStream handles stateful PNG frame decoding.
type DecodeStream struct {
	ctx *C.StreamingDecoder
}

func (nc *NexusCore) InitDecodeStream(mode int) (*DecodeStream, error) {
	ctx := C.nexus_decode_stream_init(C.int32_t(mode))
	if ctx == nil {
		return nil, errors.New("failed to initialize decode stream")
	}
	return &DecodeStream{ctx: ctx}, nil
}

func (s *DecodeStream) Push(data []byte) error {
	var inPtr *C.uint8_t
	if len(data) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	res := C.nexus_decode_stream_push(s.ctx, inPtr, C.size_t(len(data)))
	if res != C.NEXUS_OK {
		return fmt.Errorf("decode push error (code %d)", res)
	}
	return nil
}

func (s *DecodeStream) Pop() ([]byte, error) {
	var outPtr *C.uint8_t
	var outLen C.size_t
	res := C.nexus_decode_stream_pop(s.ctx, &outPtr, &outLen)
	if res == 1 {
		return nil, nil // No data ready
	}
	if res != C.NEXUS_OK {
		return nil, fmt.Errorf("decode pop error (code %d)", res)
	}
	defer C.nexus_free_bytes(outPtr, outLen)
	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}

func (s *DecodeStream) Close() {
	if s.ctx != nil {
		C.nexus_decoder_stream_drop(s.ctx)
		s.ctx = nil
	}
}
