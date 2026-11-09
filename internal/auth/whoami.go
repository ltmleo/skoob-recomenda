package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIBase é a API usada pelo site do Skoob (não documentada publicamente).
const APIBase = "https://prd-api.skoob.com.br/api"

// Profile faz GET /v1/user/profile com o token, validando a sessão.
// Retorna o JSON bruto: o formato da resposta ainda não foi mapeado.
func Profile(ctx context.Context, base, token string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/user/profile", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token) // o site envia o token cru, sem "Bearer"
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("sessão inválida ou expirada (HTTP %d): rode `recomenda login`", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("resposta não-JSON: %w", err)
	}
	return out, nil
}

// UserID extrai o ID do usuário do perfil ({"user":{"id":"..."}}).
func UserID(p map[string]any) string {
	u, _ := p["user"].(map[string]any)
	id, _ := u["id"].(string)
	return id
}

// MyID valida a sessão e devolve o ID do usuário logado.
func MyID(ctx context.Context, base, token string) (string, error) {
	p, err := Profile(ctx, base, token)
	if err != nil {
		return "", err
	}
	if id := UserID(p); id != "" {
		return id, nil
	}
	return "", errors.New("não consegui ler seu ID no perfil do Skoob")
}
