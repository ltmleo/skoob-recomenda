package auth

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"net/url"
	"testing"
)

const testJWT = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IngifQ.c2ln"

// wrap reproduz o formato do cookie do Skoob: base64(zlib(JSON string)).
func wrap(t *testing.T, jwt string) string {
	t.Helper()
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write([]byte(`"` + jwt + `"`))
	w.Close()
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestUnwrapToken(t *testing.T) {
	c := wrap(t, testJWT)
	cases := map[string]string{
		"cookie":            c,
		"cookie urlencoded": url.QueryEscape(c),
		"cookie sem pad":    base64.RawStdEncoding.EncodeToString(mustDecode(t, c)),
		"jwt cru":           testJWT,
		"jwt com espaço":    " " + testJWT + "\n",
	}
	for name, in := range cases {
		if got := UnwrapToken(in); got != testJWT {
			t.Errorf("%s: got %q", name, got)
		}
	}
	if got := UnwrapToken("opaco"); got != "opaco" {
		t.Errorf("valor desconhecido deve voltar intacto, got %q", got)
	}
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
