package main

import (
    "crypto/rand"
    "encoding/hex"
    "io/ioutil"
    "os"
    "testing"
)

func TestEncryptDecryptAndHash(t *testing.T) {
    // Create a temporary plaintext file
    tmp, err := ioutil.TempFile("", "nexus_plain_*.bin")
    if err != nil {
        t.Fatalf("tmp create: %v", err)
    }
    defer os.Remove(tmp.Name())
    data := []byte("this is a test payload for snapshot encryption")
    if _, err := tmp.Write(data); err != nil {
        t.Fatalf("write tmp: %v", err)
    }
    tmp.Close()

    // generate random 32-byte key
    key := make([]byte, 32)
    if _, err := rand.Read(key); err != nil {
        t.Fatalf("key gen: %v", err)
    }

    encPath := tmp.Name() + ".enc"
    decPath := tmp.Name() + ".dec"
    defer os.Remove(encPath)
    defer os.Remove(decPath)

    if err := encryptFileAESGCM(tmp.Name(), encPath, key); err != nil {
        t.Fatalf("encrypt failed: %v", err)
    }

    if err := decryptFileAESGCM(encPath, decPath, key); err != nil {
        t.Fatalf("decrypt failed: %v", err)
    }

    // compare files
    got, err := os.ReadFile(decPath)
    if err != nil {
        t.Fatalf("read dec: %v", err)
    }
    if string(got) != string(data) {
        t.Fatalf("decrypted content mismatch: got=%s", string(got))
    }

    // ensure calculateFileHash runs and returns hex
    if h, err := (&SyncManager{}).calculateFileHash(tmp.Name()); err != nil {
        t.Fatalf("hash plaintext failed: %v", err)
    } else {
        if _, err := hex.DecodeString(h); err != nil {
            t.Fatalf("hash not hex: %v", err)
        }
    }
}
