package auth

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func testEncryptDecryptRange(t *testing.T, plaintext []byte, pStart, pEnd int64) {
	t.Helper()

	key := make([]byte, 32)
	rand.Read(key)

	var cipherBuf bytes.Buffer
	if err := EncryptStream(key, bytes.NewReader(plaintext), &cipherBuf); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ciphertext := cipherBuf.Bytes()

	chunkSz := int64(DefaultChunkSize)
	encChunkSz := int64(NonceSize + DefaultChunkSize + TagSize)
	headerSz := int64(5)

	startChunk := pStart / chunkSz
	endChunk := pEnd / chunkSz

	cipherStart := headerSz + startChunk*encChunkSz
	cipherEnd := headerSz + (endChunk+1)*encChunkSz - 1
	if cipherEnd >= int64(len(ciphertext)) {
		cipherEnd = int64(len(ciphertext)) - 1
	}

	chunkData := ciphertext[cipherStart : cipherEnd+1]

	trimStart := pStart - startChunk*chunkSz
	contentLength := pEnd - pStart + 1
	trimEnd := trimStart + contentLength - 1

	var result bytes.Buffer
	if err := DecryptRange(key, bytes.NewReader(chunkData), &result, DefaultChunkSize, uint64(startChunk), trimStart, trimEnd); err != nil {
		t.Fatalf("decrypt range: %v", err)
	}

	expected := plaintext[pStart : pEnd+1]
	if !bytes.Equal(result.Bytes(), expected) {
		t.Fatalf("mismatch: got %d bytes, want %d bytes", result.Len(), len(expected))
	}
}

func TestDecryptRange_SingleChunk(t *testing.T) {
	plaintext := make([]byte, 1000)
	rand.Read(plaintext)
	testEncryptDecryptRange(t, plaintext, 100, 500)
}

func TestDecryptRange_FullSmallFile(t *testing.T) {
	plaintext := make([]byte, 500)
	rand.Read(plaintext)
	testEncryptDecryptRange(t, plaintext, 0, 499)
}

func TestDecryptRange_MultiChunk(t *testing.T) {
	plaintext := make([]byte, 150000)
	rand.Read(plaintext)
	testEncryptDecryptRange(t, plaintext, 60000, 70000)
}

func TestDecryptRange_LastChunkPartial(t *testing.T) {
	plaintext := make([]byte, 65536+10)
	rand.Read(plaintext)
	testEncryptDecryptRange(t, plaintext, 65536, 65545)
}

func TestDecryptRange_EntireMultiChunkFile(t *testing.T) {
	plaintext := make([]byte, 200000)
	rand.Read(plaintext)
	testEncryptDecryptRange(t, plaintext, 0, int64(len(plaintext))-1)
}

func TestDecryptRange_SingleByte(t *testing.T) {
	plaintext := make([]byte, 100)
	rand.Read(plaintext)
	testEncryptDecryptRange(t, plaintext, 50, 50)
}

func TestDecryptRange_CrossThreeChunks(t *testing.T) {
	plaintext := make([]byte, 200000)
	rand.Read(plaintext)
	testEncryptDecryptRange(t, plaintext, 10000, 140000)
}
