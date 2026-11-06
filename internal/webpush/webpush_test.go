package webpush

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The worked example of RFC 8291, Appendix A.
func TestSealMatchesRFC8291(t *testing.T) {
	got, err := seal(
		b64(t, "kyrL1jIIOHEzg3sM2ZWRHDRB62YACZhhSlknJ672kSs"),
		b64(t, "BTBZMqHH6r4Tts7J_aSIgg"),
		b64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"),
		b64(t, "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"),
		b64(t, "DGv6ra1nlYgDCS1FRnbzlw"),
		[]byte("When I grow up, I want to be a watermelon"),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := b64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN")
	if !bytes.Equal(got, want) {
		t.Fatalf("seal = %x\nwant  %x", got, want)
	}
}

func TestCheckEndpoint(t *testing.T) {
	for _, ok := range []string{"https://fcm.googleapis.com/fcm/send/abc", "https://updates.push.services.mozilla.com/wpush/v2/x", "https://web.push.apple.com/Q", "https://wns2-par02p.notify.windows.com/w/?token=x"} {
		if err := CheckEndpoint(ok); err != nil {
			t.Errorf("CheckEndpoint(%s) = %v", ok, err)
		}
	}
	for _, bad := range []string{"http://fcm.googleapis.com/x", "https://169.254.169.254/latest", "https://evil.com/fcm.googleapis.com", "https://fcm.googleapis.com.evil.com/x", "https://fcm.googleapis.com:8443/x", "https://user@fcm.googleapis.com/x", "https://pushapple.com/x", "not a url"} {
		if err := CheckEndpoint(bad); err == nil {
			t.Errorf("CheckEndpoint(%s) accepted it", bad)
		}
	}
}

func TestSendSignsAndHandlesGone(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSender(key, "mailto:ops@greyship.tech", nil, func() time.Time { return time.Unix(1700000000, 0) })
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.authorization("https://fcm.googleapis.com/fcm/send/abc")
	if err != nil {
		t.Fatal(err)
	}
	jwt, k, ok := strings.Cut(strings.TrimPrefix(auth, "vapid t="), ", k=")
	if !ok || k != s.PublicKey() {
		t.Fatalf("authorization = %q", auth)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 || !strings.Contains(string(b64(t, parts[1])), `"aud":"https://fcm.googleapis.com"`) {
		t.Fatalf("jwt = %q", jwt)
	}
	sig := b64(t, parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), b64(t, k))
	if err != nil {
		t.Fatal(err)
	}
	if !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the VAPID signature doesn't verify with our public key")
	}

	// A push service answering 410 means the browser unsubscribed.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(r.Header.Get("Authorization"), "vapid t=") {
			t.Errorf("headers = %v", r.Header)
		}
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	s.client = srv.Client()
	sub := Subscription{Endpoint: srv.URL, P256dh: "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4", Auth: "BTBZMqHH6r4Tts7J_aSIgg"}
	if err := s.Send(context.Background(), sub, []byte("hi")); err == nil || errors.Is(err, ErrGone) {
		t.Fatalf("a test server isn't a push service and must be refused, got %v", err)
	}
	if err := s.deliver(context.Background(), sub, []byte("hi")); !errors.Is(err, ErrGone) {
		t.Fatalf("410 from the push service = %v, want ErrGone", err)
	}
}
