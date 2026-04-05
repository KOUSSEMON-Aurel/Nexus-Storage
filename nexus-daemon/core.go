package main

/*
#cgo LDFLAGS: ${SRCDIR}/libnexus_core.a -lm -lpthread
#cgo linux LDFLAGS: -ldl
#cgo windows LDFLAGS: -lws2_32 -luserenv -lbcrypt -lntdll
#cgo CFLAGS: -I${SRCDIR}/../nexus-core/include
#include <stdlib.h>
#include "nexus_core.h"
*/
import "C"
import (
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

