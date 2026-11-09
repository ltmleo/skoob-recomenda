#!/usr/bin/env bash
# Calcula a próxima versão (SemVer) a partir dos Conventional Commits
# desde a última tag (vX.Y.Z), e escreve as notas de release.
#
#   scripts/next-version.sh                 # imprime ex.: 0.3.0 (vazio = nada a lançar)
#   NOTES=notas.md scripts/next-version.sh  # também grava as notas em notas.md
#
# Regras (https://www.conventionalcommits.org):
#   feat:                       -> minor
#   fix: / perf:                -> patch
#   tipo!: ou "BREAKING CHANGE" -> major (antes da 1.0.0, minor)
#   docs:, test:, ci:, chore:, refactor:, style:, build: -> não gera release
set -euo pipefail

prefix="${TAG_PREFIX:-v}"
path="${MODULE_PATH:-.}"

last="$(git describe --tags --match "${prefix}[0-9]*" --abbrev=0 2>/dev/null || true)"
if [[ -n "$last" ]]; then
  range="${last}..HEAD"
  current="${last#"$prefix"}"
else
  range="HEAD"
  current="0.0.0"
fi
IFS=. read -r major minor patch <<<"$current"

bump=0 # 0 nada, 1 patch, 2 minor, 3 major
feats="" fixes="" perfs="" breaks=""
re='^([a-z]+)(\([^)]*\))?(!)?: (.+)$'

while IFS= read -r -d $'\x1e' entry; do
  entry="${entry#$'\n'}"
  [[ -z "$entry" ]] && continue
  hash="${entry%%$'\x1f'*}"
  rest="${entry#*$'\x1f'}"
  subject="${rest%%$'\n'*}"
  body="${rest#"$subject"}"
  [[ "$subject" =~ $re ]] || continue
  type="${BASH_REMATCH[1]}"
  scope="${BASH_REMATCH[2]}"
  bang="${BASH_REMATCH[3]}"
  desc="${BASH_REMATCH[4]}"
  scope="${scope#(}"
  scope="${scope%)}"
  line="- ${scope:+**${scope}:** }${desc} (${hash:0:7})"$'\n'
  if [[ -n "$bang" || "$body" == *"BREAKING CHANGE"* ]]; then
    breaks+="$line"
    bump=3
  fi
  case "$type" in
    feat) feats+="$line"; ((bump < 2)) && bump=2 ;;
    fix) fixes+="$line"; ((bump < 1)) && bump=1 ;;
    perf) perfs+="$line"; ((bump < 1)) && bump=1 ;;
  esac
done < <(git log --format='%H%x1f%s%n%b%x1e' "$range" -- "$path")

case "$bump" in
  0) exit 0 ;; # nada a lançar
  1) patch=$((patch + 1)) ;;
  2) minor=$((minor + 1)); patch=0 ;;
  3)
    if ((major == 0)); then
      minor=$((minor + 1)); patch=0 # 0.x: mudança incompatível sobe a minor
    else
      major=$((major + 1)); minor=0; patch=0
    fi
    ;;
esac
next="${major}.${minor}.${patch}"

if [[ -n "${NOTES:-}" ]]; then
  {
    [[ -n "$breaks" ]] && printf '## ⚠️ Mudanças incompatíveis\n\n%s\n' "$breaks"
    [[ -n "$feats" ]] && printf '## Novidades\n\n%s\n' "$feats"
    [[ -n "$fixes" ]] && printf '## Correções\n\n%s\n' "$fixes"
    [[ -n "$perfs" ]] && printf '## Desempenho\n\n%s\n' "$perfs"
    if [[ -n "$last" ]]; then
      printf '**Comparação:** https://github.com/%s/compare/%s...%s%s\n' \
        "${GITHUB_REPOSITORY:-ltmleo/skoob-recomenda}" "$last" "$prefix" "$next"
    fi
  } >"$NOTES"
fi
echo "$next"
