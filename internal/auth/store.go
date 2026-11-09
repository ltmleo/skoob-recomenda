// Package auth obtém e guarda o token de sessão do Skoob.
package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvToken permite injetar o token sem arquivo (CI, ambientes remotos).
const EnvToken = "SKOOB_TOKEN"

// Store persiste o token em um arquivo legível só pelo dono (0600).
type Store struct{ Path string }

// DefaultStore usa ~/.config/recomendador/token (ou equivalente do SO).
func DefaultStore() (Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Path: filepath.Join(dir, "recomendador", "token")}, nil
}

func (s Store) Save(token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("token vazio")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	// Escreve em arquivo temporário e renomeia: nunca deixa token pela metade.
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Load retorna o token de SKOOB_TOKEN ou do arquivo, já desembrulhado (ver UnwrapToken).
func (s Store) Load() (string, error) {
	if t := strings.TrimSpace(os.Getenv(EnvToken)); t != "" {
		return UnwrapToken(t), nil
	}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("sem login: rode `recomenda login` (ou defina %s)", EnvToken)
	}
	if err != nil {
		return "", err
	}
	return UnwrapToken(string(b)), nil
}

func (s Store) Delete() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
