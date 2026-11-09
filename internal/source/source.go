// Package source define de onde vêm as estantes (Skoob, CSV, cache...).
package source

import (
	"context"

	"github.com/ltmleo/skoob-recomenda/internal/domain"
)

// Source abstrai a origem dos dados. O crawler só conhece esta interface,
// o que permite trocar o Skoob por CSV/mocks sem tocar no algoritmo.
type Source interface {
	// UserRatings retorna as leituras de um usuário.
	UserRatings(ctx context.Context, userID string) ([]domain.Rating, error)
	// BookReaders retorna até limit usuários que leram o livro.
	BookReaders(ctx context.Context, bookID string, limit int) ([]string, error)
	// Book retorna metadados do livro.
	Book(ctx context.Context, bookID string) (domain.Book, error)
}
