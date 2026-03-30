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

// --- Envelope encryption (DEK wrap/unwrap) tests ---

func TestGenerateDEK_Returns32Bytes(t *testing.T) {
	dek, err := GenerateDEK()
	if err != nil {
		t.Fatalf("GenerateDEK: %v", err)
	}
	if len(dek) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(dek))
	}
}

func TestGenerateDEK_Unique(t *testing.T) {
	dek1, err := GenerateDEK()
	if err != nil {
		t.Fatalf("GenerateDEK (1): %v", err)
	}
	dek2, err := GenerateDEK()
	if err != nil {
		t.Fatalf("GenerateDEK (2): %v", err)
	}
	if bytes.Equal(dek1, dek2) {
		t.Fatal("two GenerateDEK calls returned identical values")
	}
}

func TestWrapUnwrapDEK_RoundTrip(t *testing.T) {
	kek := make([]byte, 32)
	rand.Read(kek)

	dek := make([]byte, 32)
	rand.Read(dek)

	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}

	unwrapped, err := UnwrapDEK(kek, wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}

	if !bytes.Equal(dek, unwrapped) {
		t.Fatal("unwrapped DEK does not match original")
	}
}

func TestUnwrapDEK_WrongKEK(t *testing.T) {
	kek := make([]byte, 32)
	rand.Read(kek)

	dek := make([]byte, 32)
	rand.Read(dek)

	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}

	wrongKEK := make([]byte, 32)
	rand.Read(wrongKEK)

	_, err = UnwrapDEK(wrongKEK, wrapped)
	if err == nil {
		t.Fatal("expected error when unwrapping with wrong KEK")
	}
}

func TestWrapDEK_OutputSize(t *testing.T) {
	kek := make([]byte, 32)
	rand.Read(kek)

	dek := make([]byte, 32)
	rand.Read(dek)

	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}

	// Expected: 12 (nonce) + 32 (ciphertext) + 16 (tag) = 60 bytes
	if len(wrapped) != 60 {
		t.Fatalf("expected wrapped DEK to be 60 bytes, got %d", len(wrapped))
	}
}

func TestGenerateKEK_Returns32Bytes(t *testing.T) {
	kek, err := GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	if len(kek) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(kek))
	}
}

func TestWrapUnwrapKEK_RoundTrip(t *testing.T) {
	serverKey := make([]byte, 32)
	rand.Read(serverKey)

	kek, _ := GenerateKEK()

	wrapped, err := WrapKEK(serverKey, kek)
	if err != nil {
		t.Fatalf("WrapKEK: %v", err)
	}

	unwrapped, err := UnwrapKEK(serverKey, wrapped)
	if err != nil {
		t.Fatalf("UnwrapKEK: %v", err)
	}

	if !bytes.Equal(kek, unwrapped) {
		t.Fatal("unwrapped KEK does not match original")
	}
}

func TestServerKeyFromSecret(t *testing.T) {
	// 64 hex chars = 32 bytes
	secret := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key, err := ServerKeyFromSecret(secret)
	if err != nil {
		t.Fatalf("ServerKeyFromSecret: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(key))
	}

	// Too short
	_, err = ServerKeyFromSecret("0123")
	if err == nil {
		t.Fatal("expected error for short secret")
	}
}

func TestEnvelopeEncryption_EndToEnd(t *testing.T) {
	// 1. Generate a DEK
	dek, err := GenerateDEK()
	if err != nil {
		t.Fatalf("GenerateDEK: %v", err)
	}

	// 2. Encrypt data with the DEK
	original := []byte("the quick brown fox jumps over the lazy dog")
	ciphertext, err := EncryptBytes(dek, original)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}

	// 3. Wrap the DEK with a KEK
	kek := make([]byte, 32)
	rand.Read(kek)

	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}

	// 4. Unwrap the DEK
	unwrapped, err := UnwrapDEK(kek, wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}

	// 5. Decrypt data with the unwrapped DEK
	plaintext, err := DecryptBytes(unwrapped, ciphertext)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}

	if !bytes.Equal(original, plaintext) {
		t.Fatalf("end-to-end mismatch: got %q, want %q", plaintext, original)
	}
}
