package auth

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"strings"
)

// UnwrapToken extrai o JWT do cookie `token` do Skoob. O site não guarda o
// JWT direto: o cookie é base64(zlib(JSON string com o JWT)), e a API só
// aceita o JWT. Valores que já são JWT (ou não decodificam) voltam intactos,
// então dá para chamar sobre tokens antigos ou vindos de SKOOB_TOKEN.
func UnwrapToken(v string) string {
	v = strings.TrimSpace(v)
	if looksLikeJWT(v) {
		return v
	}
	s := v
	if u, err := url.QueryUnescape(s); err == nil {
		s = u
	}
	s = strings.TrimRight(s, "=")
	raw, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		if raw, err = base64.RawURLEncoding.DecodeString(s); err != nil {
			return v
		}
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return v
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, 64<<10))
	if err != nil {
		return v
	}
	var jwt string
	if err := json.Unmarshal(out, &jwt); err != nil || !looksLikeJWT(jwt) {
		return v
	}
	return jwt
}

func looksLikeJWT(s string) bool {
	return strings.HasPrefix(s, "eyJ") && strings.Count(s, ".") == 2
}
