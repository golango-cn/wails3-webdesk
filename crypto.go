package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

// machineKey derives a 32-byte AES key from machine-specific identifiers.
// Bind to hostname + first MAC address so the key is unique per machine.
func machineKey() []byte {
	h := sha256.New()
	hostname, _ := os.Hostname()
	h.Write([]byte(hostname))

	interfaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range interfaces {
			// Skip loopback and down interfaces
			if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
				continue
			}
			if len(iface.HardwareAddr) > 0 {
				h.Write(iface.HardwareAddr)
				break
			}
		}
	}

	key := h.Sum(nil)
	return key[:32] // AES-256
}

// encrypt encrypts plaintext using AES-GCM with the machine-derived key.
// Returns hex-encoded nonce+ciphertext.
func encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	key := machineKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(sealed), nil
}

// decrypt decrypts hex-encoded nonce+ciphertext using AES-GCM with the machine-derived key.
func decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	data, err := hex.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("hex decode: %w", err)
	}

	key := machineKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("cipher.NewGCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ct := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// matchURL checks if a site URL matches a given URL by comparing scheme+host.
// Supports matching "https://mail.company.com" against "https://mail.company.com/login".
func matchURL(siteURL, pageURL string) bool {
	siteURL = strings.TrimRight(siteURL, "/")
	pageURL = strings.TrimRight(pageURL, "/")

	// Exact match
	if siteURL == pageURL {
		return true
	}

	// Prefix match: site URL is a prefix of page URL
	if strings.HasPrefix(pageURL, siteURL+"/") || strings.HasPrefix(pageURL, siteURL+"?") {
		return true
	}

	return false
}
