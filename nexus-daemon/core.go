package main

/*
#cgo LDFLAGS: -L${SRCDIR}/../target/debug -lnexus_core
#cgo CFLAGS: -I${SRCDIR}/../nexus-core/include
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

func main() {
	fmt.Println("Nexus-Daemon: testing core bindings...")
	// This main function is temporary for testing.
}
