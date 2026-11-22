# Security policy

## Supported versions

Only the [latest release](https://github.com/motivated2die/flit/releases/latest) receives fixes. Please check that a problem still exists there before reporting it.

## Reporting a vulnerability

Please **don't** open a public issue for security problems. Report them privately through GitHub instead: **Security** tab → **Report a vulnerability** ([direct link](https://github.com/motivated2die/flit/security/advisories/new)).

Helpful details: the flit version, what an attacker can do and from where (the same PC, the network), and steps to reproduce it.

You can expect a first answer within a week. Once a fix is released, the advisory is published, crediting you if you like.

## Scope

In scope, for example: the installer and updater (`internal/install`, `internal/update`: the download from GitHub and its checksum check), downloads of the engine and models (`internal/download`, `internal/models`: pinned URLs and SHA-256), the engine worker process and its pipe protocol (`internal/engine`), the calendar feed (`internal/calendar`: the subscription link is a secret and must not leak), and anything that would let recordings or transcripts leave the PC or reach the log.

flit opens no listening port and runs without admin rights; a way around either is in scope too.
