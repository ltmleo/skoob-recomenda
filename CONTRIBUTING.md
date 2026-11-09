# Contribuindo

Obrigado pelo interesse! Este guia explica como o projeto é desenvolvido e
publicado. Resumo: **trunk-based development**, **Conventional Commits** e
**release automático a partir da `main`**.

## Fluxo de trabalho (trunk-based)

A `main` é o tronco: está sempre verde e pronta para release. Não existem
branches de `develop`, `release/*` ou `hotfix/*`.

1. Crie uma branch curta a partir da `main` (`git switch -c feat/filtro-autor`).
   Ela deve viver horas ou poucos dias, não semanas.
2. Faça commits pequenos e abra um Pull Request para a `main` cedo, mesmo
   como rascunho.
3. O CI roda lint e testes em Linux, macOS e Windows e confere o título do PR.
4. O merge é sempre **squash**: o título do PR vira o único commit na `main`.
5. Ao entrar na `main`, o pipeline decide sozinho se gera uma versão nova.

Funcionalidades grandes entram aos poucos, em PRs pequenos. Se algo ainda não
está pronto para o usuário, deixe-o atrás de uma flag de linha de comando
desligada por padrão, em vez de segurar uma branch longa.

## Título do PR (Conventional Commits)

O título do PR define a versão do próximo release:

| Título                                   | Efeito na versão           |
|------------------------------------------|----------------------------|
| `feat(web): filtro por autor`            | minor (`0.3.0` → `0.4.0`)  |
| `fix(crawl): semente sem edição`         | patch (`0.3.0` → `0.3.1`)  |
| `perf(recommend): índice item-item`      | patch                      |
| `feat!: novo formato de CSV`             | major (antes da 1.0, minor)|
| `docs:`, `test:`, `ci:`, `refactor:`, `chore:`, `build:`, `style:` | sem release |

Uma mudança incompatível também pode ser marcada com `BREAKING CHANGE: ...`
no corpo do commit. Escopos comuns: `web`, `recommend`, `crawl`, `source`,
`auth`, `eval`, `cli`, `deps`.

## Ambiente de desenvolvimento

Requer **Go 1.26+**. O Chrome/Chromium só é necessário para `recomenda login`
e para os testes de navegador (pulados quando `CHROME_PATH` não está definido).

```bash
go test ./...                     # testes
go test -race ./...               # com detector de corrida (precisa de cgo)
go vet ./...
gofmt -l .                        # deve sair vazio
go run ./cmd/recomenda eval       # qualidade das recomendações
```

### Mudanças no algoritmo

Toda mudança em `internal/recommend` deve vir com o antes e o depois de
`recomenda eval` no PR, de preferência nos cortes aleatório e `-temporal`
(`-users -1`). Uma ideia que não melhora as métricas pode entrar desligada,
como opção, mas não deve virar o padrão.

### Dados

**Nunca** commite dados reais do Skoob (`skoob.csv`, estantes, tokens). Eles
estão no `.gitignore`. Testes usam dados sintéticos escritos no próprio teste
ou em `testdata/`.

## Release

O release é feito pelo job `release` de
[`.github/workflows/ci.yml`](.github/workflows/ci.yml), e só na `main`:

1. `scripts/next-version.sh` lê os commits desde a última tag `vX.Y.Z` e
   calcula a próxima versão.
2. Sem `feat`/`fix`/`perf`/breaking, não há release.
3. Com eles, compila binários para Linux, macOS e Windows (amd64 e arm64),
   gera `checksums.txt` e as notas de release, cria a tag e publica o
   GitHub Release.

Para conferir localmente que versão sairia:

```bash
NOTES=/tmp/notas.md bash scripts/next-version.sh && cat /tmp/notas.md
```

### Configuração recomendada do repositório

- Proteger a `main`: exigir PR, os checks `Lint` e `Testes`, e histórico linear.
- Em *Settings → General → Pull Requests*: permitir só **squash merge**, com a
  mensagem padrão usando o título do PR.
