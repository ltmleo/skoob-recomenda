# Política de segurança

## Versões suportadas

Só a versão mais recente (o último release `vX.Y.Z`) recebe
correções. O desenvolvimento é trunk-based: correções saem num novo release
patch, a partir da `main`.

## Como reportar uma vulnerabilidade

**Não abra uma issue pública.** Use o
[reporte privado de vulnerabilidades do GitHub](https://github.com/ltmleo/skoob-recomenda/security/advisories/new),
com:

- o que um atacante consegue fazer e em que condições;
- passos para reproduzir (versão, sistema operacional, comando);
- se possível, uma sugestão de correção.

A ideia é responder em até 7 dias e publicar a correção junto com um aviso
de segurança (GitHub Security Advisory).

## Modelo de ameaça

O `recomenda` lida com o **token de sessão do Skoob**, que dá acesso total à
conta do usuário. Os pontos sensíveis, e como são tratados:

| Ativo / superfície         | Proteção                                                                 |
|----------------------------|--------------------------------------------------------------------------|
| Senha do Skoob             | Nunca passa pela CLI: é digitada no site, num Chrome com perfil temporário. |
| Token salvo                | `~/.config/recomendador/token` com permissão `0600` (no Windows, a ACL do perfil). |
| App local (`serve`)        | Escuta em `127.0.0.1`; recusa `Host` que não seja loopback (DNS rebinding) e requisições de outra origem que alteram dados (CSRF, via `http.CrossOriginProtection`). |
| Dados coletados            | Ficam só na máquina (`skoob.csv`), fora do controle de versão.           |
| Dependências               | `govulncheck` no CI e Dependabot semanal.                                |

São especialmente bem-vindos reportes sobre vazamento do token, formas de um
site externo controlar o app local, ou de fazer a CLI enviar dados para
outro destino que não a API do Skoob.
