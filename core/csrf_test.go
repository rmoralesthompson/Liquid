package liquid

import (
	"strings"
	"testing"
	"time"
)

// The codec is tested white-box because expiry needs a controllable clock;
// the wire-level accept/reject behavior is covered at the HTTP seam in
// hydro_test.go and via liquidtest.

func TestMintedCSRFTokenValidatesForItsSession(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1_700_000_000, 0)

	token := mintCSRF(secret, "sess-a", time.Hour, now)

	if !validCSRF(secret, token, "sess-a", now.Add(time.Minute)) {
		t.Errorf("freshly minted token %q does not validate for its own session", token)
	}
	if strings.Contains(token, "sess-a") {
		t.Errorf("token %q embeds the session ID; the encoding is signature-only — expiry:signature (D15 as amended by #45)", token)
	}
}

func TestCSRFTokenIsRejectedForAForeignSession(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1_700_000_000, 0)

	token := mintCSRF(secret, "sess-a", time.Hour, now)

	if validCSRF(secret, token, "sess-b", now) {
		t.Error("token minted for sess-a validated under sess-b; tokens are session-bound")
	}
}

func TestCSRFTokenIsRejectedAfterItsExpiry(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1_700_000_000, 0)

	token := mintCSRF(secret, "sess-a", time.Hour, now)

	if validCSRF(secret, token, "sess-a", now.Add(time.Hour+time.Second)) {
		t.Error("token validated past its expiry window")
	}
}

func TestTamperedOrGarbageCSRFTokensAreRejected(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1_700_000_000, 0)
	token := mintCSRF(secret, "sess-a", time.Hour, now)

	tampered := strings.TrimRight(token, "0123456789abcdef") + "ffffffff" //gitleaks:allow — a deliberately broken signature, not a credential
	for _, bad := range []string{"", "not-a-token", "99:zz", "sess-a:99:zz", tampered} {
		if validCSRF(secret, bad, "sess-a", now) {
			t.Errorf("token %q validated; want rejection", bad)
		}
	}
}

func TestCSRFTokenFromAnotherSecretIsRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	token := mintCSRF([]byte("secret-one"), "sess-a", time.Hour, now)

	if validCSRF([]byte("secret-two"), token, "sess-a", now) {
		t.Error("token signed under another secret validated; signatures must be checked")
	}
}
