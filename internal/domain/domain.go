// Package domain contém os tipos centrais do recomendador.
package domain

import "time"

// Book é um livro (no Skoob, o ID corresponde ao livro, não à edição).
type Book struct {
	ID        string
	Title     string
	Author    string
	EditionID string // uma edição conhecida (Skoob consulta leitores por edição)
	Cover     string // URL da capa
}

// Status de um livro na estante (valores do Skoob).
const (
	StatusRead       = "read"
	StatusReading    = "reading"
	StatusWantToRead = "want_to_read"
	StatusAbandoned  = "abandoned"
)

// Rating é um livro na estante de um usuário. Stars == 0 significa "sem nota".
// Status vazio equivale a StatusRead (CSVs antigos).
type Rating struct {
	UserID string
	BookID string
	Stars  float64 // 0.5..5 (escala do Skoob); 0 = sem nota
	ReadAt time.Time
	Status string
}

// Rated informa se a leitura tem nota explícita.
func (r Rating) Rated() bool { return r.Stars > 0 }

// Consumed informa se o usuário já passou pelo livro (leu ou abandonou):
// esses não devem ser recomendados. "Lendo" e "quero ler" não contam.
func (r Rating) Consumed() bool {
	switch r.Status {
	case StatusReading, StatusWantToRead:
		return r.Rated()
	}
	return true
}
