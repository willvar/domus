package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/hkdf"
)

const (
	CryptoVersion    byte = 0x01
	DefaultChunkSize      = 64 * 1024 // 64KB
	NonceSize             = 12
	TagSize               = 16
)

// DeriveKey derives a per-user AES-256 key from the master encryption secret and user ID.
func DeriveKey(encryptionSecret, userID string) ([]byte, error) {
	ikm, err := hex.DecodeString(encryptionSecret)
	if err != nil {
		return nil, fmt.Errorf("decode encryption secret: %w", err)
	}

	salt := []byte(userID)
	info := []byte("zephyr-file-encryption")
	hkdfReader := hkdf.New(sha256.New, ikm, salt, info)

	key := make([]byte, 32) // AES-256
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	return key, nil
}

// EncryptStream encrypts data from r and writes ciphertext to w using chunked AES-256-GCM.
//
// Wire format:
//
//	[1 byte version] [4 bytes chunk size (big-endian)]
//	[chunk 0: 12-byte nonce | ciphertext+tag] ...
func EncryptStream(key []byte, r io.Reader, w io.Writer) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	// Write header
	header := [5]byte{CryptoVersion}
	binary.BigEndian.PutUint32(header[1:], uint32(DefaultChunkSize))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}

	buf := make([]byte, DefaultChunkSize)
	nonce := make([]byte, NonceSize)
	var chunkIdx uint64

	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			// Generate random nonce for this chunk
			if _, err := rand.Read(nonce); err != nil {
				return fmt.Errorf("generate nonce: %w", err)
			}

			// AAD = chunk index (prevents reorder/truncation)
			aad := make([]byte, 8)
			binary.BigEndian.PutUint64(aad, chunkIdx)

			// Write nonce
			if _, err := w.Write(nonce); err != nil {
				return err
			}

			// Encrypt and write ciphertext+tag
			ciphertext := gcm.Seal(nil, nonce, buf[:n], aad)
			if _, err := w.Write(ciphertext); err != nil {
				return err
			}

			chunkIdx++
		}

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

// DecryptStream decrypts chunked AES-256-GCM ciphertext from r and writes plaintext to w.
func DecryptStream(key []byte, r io.Reader, w io.Writer) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	// Read header
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if header[0] != CryptoVersion {
		return fmt.Errorf("unsupported crypto version: %d", header[0])
	}
	chunkSz := int(binary.BigEndian.Uint32(header[1:]))

	// Buffer for nonce + ciphertext + tag
	encChunkBuf := make([]byte, NonceSize+chunkSz+TagSize)
	var chunkIdx uint64

	for {
		// Read nonce
		if _, err := io.ReadFull(r, encChunkBuf[:NonceSize]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return err
		}

		// Read ciphertext+tag — use ReadFull to handle partial network reads.
		// Last chunk may be shorter, which gives ErrUnexpectedEOF.
		n, readErr := io.ReadFull(r, encChunkBuf[NonceSize:])
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		if n == 0 {
			break
		}

		nonce := encChunkBuf[:NonceSize]
		ciphertext := encChunkBuf[NonceSize : NonceSize+n]

		aad := make([]byte, 8)
		binary.BigEndian.PutUint64(aad, chunkIdx)

		plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
		if err != nil {
			return fmt.Errorf("decrypt chunk %d: %w", chunkIdx, err)
		}

		if _, err := w.Write(plaintext); err != nil {
			return err
		}

		chunkIdx++

		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	return nil
}

// EncryptFile encrypts inputPath to outputPath.
func EncryptFile(key []byte, inputPath, outputPath string) (retErr error) {
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, in.Close()) }()

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, out.Close()) }()

	return EncryptStream(key, in, out)
}

// DecryptFile decrypts inputPath to outputPath.
func DecryptFile(key []byte, inputPath, outputPath string) (retErr error) {
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, in.Close()) }()

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, out.Close()) }()

	return DecryptStream(key, in, out)
}

// EncryptBytes encrypts plaintext in memory.
func EncryptBytes(key, plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := EncryptStream(key, bytes.NewReader(plaintext), &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecryptBytes decrypts ciphertext in memory.
func DecryptBytes(key, ciphertext []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := DecryptStream(key, bytes.NewReader(ciphertext), &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecryptRange decrypts a subset of encrypted chunks from r and writes the plaintext
// within [trimStart, trimEnd] (relative to startChunk's plaintext start) to w.
// r must contain only the raw encrypted chunks (no 5-byte file header).
// startChunk is the index of the first chunk in r, used to compute AAD.
func DecryptRange(key []byte, r io.Reader, w io.Writer, chunkSz int, startChunk uint64, trimStart, trimEnd int64) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	encChunkBuf := make([]byte, NonceSize+chunkSz+TagSize)
	var written int64
	target := trimEnd - trimStart + 1
	chunkIdx := startChunk
	plaintextPos := int64(0) // plaintext byte offset relative to startChunk start

	for written < target {
		// Read nonce
		if _, err := io.ReadFull(r, encChunkBuf[:NonceSize]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return err
		}

		// Read ciphertext+tag (last chunk may be shorter)
		n, readErr := io.ReadFull(r, encChunkBuf[NonceSize:])
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		if n == 0 {
			break
		}

		nonce := encChunkBuf[:NonceSize]
		ciphertext := encChunkBuf[NonceSize : NonceSize+n]

		aad := make([]byte, 8)
		binary.BigEndian.PutUint64(aad, chunkIdx)

		plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
		if err != nil {
			return fmt.Errorf("decrypt chunk %d: %w", chunkIdx, err)
		}

		chunkEnd := plaintextPos + int64(len(plaintext))

		// Determine the overlap between this chunk's plaintext and [trimStart, trimEnd]
		sliceStart := int64(0)
		if trimStart > plaintextPos {
			sliceStart = trimStart - plaintextPos
		}
		sliceEnd := int64(len(plaintext))
		if trimEnd+1 < chunkEnd {
			sliceEnd = trimEnd + 1 - plaintextPos
		}

		if sliceStart < sliceEnd {
			if _, err := w.Write(plaintext[sliceStart:sliceEnd]); err != nil {
				return err
			}
			written += sliceEnd - sliceStart
		}

		plaintextPos = chunkEnd
		chunkIdx++

		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	return nil
}
