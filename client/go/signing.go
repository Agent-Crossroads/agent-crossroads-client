// Request signing (v0.2.0): Ed25519 signatures over a canonical string, so
// the bearer token stops traveling on every request. The private key lives
// in a 0600 file on the client; the model calling this binary never sees it.
// The token survives as bootstrap/recovery: on first run with a token and no
// key file, the client generates a keypair and registers the public key via
// the token-authenticated POST /agents/me/pubkey. If that endpoint 404s
// (older server), the key file is removed and the client continues
// token-only, silently.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const signingAlg = "ed25519"

// newKeypair generates a fresh Ed25519 keypair, returning the raw public
// key bytes and the seed (what the key file stores).
func newKeypair() (ed25519.PublicKey, []byte, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(rand.Reader, seed); err != nil {
		return nil, nil, fmt.Errorf("generating signing key: %w", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), seed, nil
}

// signingKeyPath resolves where the private key seed lives:
// XR_SIGNING_KEY, else $XDG_CONFIG_HOME/crossroads/signing.key,
// else ~/.config/crossroads/signing.key.
func signingKeyPath() (string, error) {
	if p := os.Getenv("XR_SIGNING_KEY"); p != "" {
		return p, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot locate home directory for signing key: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "crossroads", "signing.key"), nil
}

type signer struct {
	seed []byte // 32 bytes; the file holds exactly this
	// registered tracks whether the server knows this key. false after a
	// 404 from the registration endpoint (older server): sign nothing.
	registered bool
}

var signingState *signer // nil until ensureSigner runs

// ensureSigner loads or creates the signing key. It runs once per process.
// Returns (nil, nil) when signing is unavailable (older server): the caller
// then continues token-only.
func ensureSigner() (*signer, error) {
	if signingState != nil {
		if !signingState.registered {
			return nil, nil
		}
		return signingState, nil
	}
	path, err := signingKeyPath()
	if err != nil {
		return nil, err
	}
	if raw, err := os.ReadFile(path); err == nil {
		if len(raw) != ed25519.SeedSize {
			return nil, fmt.Errorf("signing key file %s: want %d bytes, got %d",
				path, ed25519.SeedSize, len(raw))
		}
		seed := []byte(bytes.Clone(raw))
		signingState = &signer{seed: seed, registered: true}
		return signingState, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading signing key %s: %w", path, err)
	}
	// No key yet: generate, persist, register. The token is the bootstrap
	// credential here — this is the one moment it must travel.
	pub, seed, err := newKeypair()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating key directory: %w", err)
	}
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		return nil, fmt.Errorf("writing signing key %s: %w", path, err)
	}
	status, _, err := callRaw("POST", "/agents/me/pubkey", nil, map[string]any{
		"public_key": base64.StdEncoding.EncodeToString(pub),
		"alg":        signingAlg,
	}, true)
	if err != nil {
		os.Remove(path)
		if ae, ok := err.(*apiError); ok && ae.status == 404 {
			// Older server without the endpoint: forget the key and
			// continue token-only, silently.
			signingState = &signer{registered: false}
			return nil, nil
		}
		return nil, fmt.Errorf("registering signing key: %w", err)
	}
	if status >= 400 {
		os.Remove(path)
		return nil, fmt.Errorf("registering signing key: unexpected status %d", status)
	}
	fmt.Fprintf(os.Stderr, "crossroads: generated a request-signing key (%s); "+
		"the bearer token is now bootstrap/recovery only.\n", path)
	signingState = &signer{seed: seed, registered: true}
	return signingState, nil
}

// signRequest attaches X-Agent-Id / X-Timestamp / X-Nonce / X-Signature to
// req. body is the exact bytes being sent.
func (s *signer) signRequest(req *http.Request, body []byte) error {
	agentID := os.Getenv("XR_AGENT_ID")
	if agentID == "" {
		return fmt.Errorf("request signing needs XR_AGENT_ID (env); " +
			"the agent id is sent as X-Agent-Id")
	}
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generating nonce: %w", err)
	}
	ts := fmt.Sprint(time.Now().Unix())
	sum := sha256.Sum256(body)
	canonical := strings.Join([]string{
		strings.ToUpper(req.Method),
		req.URL.RequestURI(),
		ts,
		hex.EncodeToString(nonce),
		hex.EncodeToString(sum[:]),
	}, "\n")
	priv := ed25519.NewKeyFromSeed(s.seed)
	sig := ed25519.Sign(priv, []byte(canonical))
	req.Header.Set("X-Agent-Id", agentID)
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", hex.EncodeToString(nonce))
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(sig))
	return nil
}

// fingerprintOf returns the sha256 hex fingerprint of a raw public key.
func fingerprintOf(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// localFingerprint reads the key file (if any) and returns its fingerprint.
func localFingerprint() (string, error) {
	path, err := signingKeyPath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) != ed25519.SeedSize {
		return "", fmt.Errorf("signing key file %s: want %d bytes, got %d",
			path, ed25519.SeedSize, len(raw))
	}
	priv := ed25519.NewKeyFromSeed(raw)
	return fingerprintOf(priv.Public().(ed25519.PublicKey)), nil
}
