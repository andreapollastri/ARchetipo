# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## Progetto

ARchetipo è un set di skill per AI coding agent (Claude Code, Codex, Gemini CLI, OpenCode, GitHub Copilot) che supportano il processo di ideazione, analisi e pianificazione di un progetto software.

## Struttura del repository

```
skills/                  # Skill principali (una dir per skill)
  <skill-name>/
    SKILL.md             # Definizione della skill
    references/          # File di supporto caricati dalla skill
skills-extra/            # Skill extra (stessa struttura)
.archetipo/              # File installati nel progetto target (mirror della struttura target)
  config.yaml            # Template di configurazione per il progetto target
  shared-runtime.md      # Regole condivise (Language Policy, Persona, ecc.)
cli/                     # Modulo Go che implementa la CLI `archetipo`
  cmd/archetipo/         # Entry point del binario
  internal/
    cli/                 # Sub-comandi cobra (superficie pubblica della CLI)
    domain/              # Tipi dati condivisi
    connector/           # Interfaccia + due implementazioni (filefs, github)
    config/              # Loader di .archetipo/config.yaml
    iox/                 # Envelope JSON stdin/stdout/stderr
install.ps1 / install.sh # Installer legacy (curl|bash) — alternativa senza Node.js
npm/                     # Pacchetto npm (@techreloaded/archetipo + 6 sub-package per piattaforma)
scripts/                 # Build e publish dei pacchetti npm
```

## Architettura connector

Le skill non gestiscono direttamente la persistenza e non eseguono operazioni di connector "interpretando" istruzioni. Il flusso è sempre:

1. La skill legge `.archetipo/shared-runtime.md` per envelope JSON, regole sugli errori e disciplina di invocazione.
2. La skill invoca `archetipo <subcmd>` (binario Go installato globalmente via `npm i -g @techreloaded/archetipo`).
3. La CLI legge `.archetipo/config.yaml` per scegliere il connector (`file` o `github`) ed esegue l'operazione in modo deterministico.

Le skill devono incorporare esplicitamente i sub-comandi CLI che usano davvero, con i relativi payload, envelope attesi ed `error.code` rilevanti. Non esiste un file separato che descrive l'intero protocollo.

## Regole per skill author

- Chiama solo i sub-comandi che la skill usa realmente.
- I template di contenuto (PRD, body delle storie, plan body, body dei sub-issue) sono prodotti dalla skill e passati alla CLI via stdin. La CLI persiste il payload, non lo arricchisce.
- La logica di validazione e post-processing degli output JSON va nella skill.
- I sub-comandi no-op sono espliciti: per esempio `comment post` ritorna `ok: true` anche con `connector: file`. La skill non deve mai ramificare sul tipo di connector.
- Branch sull'`error.code` del JSON envelope, non sul `message`.
- Caricare `.archetipo/shared-runtime.md` **una sola volta** all'avvio della skill.

## Regole per chi modifica la CLI

- Le 13 operazioni pubbliche della CLI sono stabili: ogni cambiamento incompatibile è un breaking change e va versionato.
- Mantenere la conformance suite (`cli/internal/connector/conformance/`) verde su tutte le implementazioni: file, github, inmemory.
- Tutte le query GraphQL del connector github vivono in `cli/internal/connector/github/templates.go`. Aggiungere snapshot test prima di modificarle.
- Distribuzione: il binario è versionato insieme alle skill (un solo tag per repo). Su tag `v*` il workflow `release.yml` esegue GoReleaser per produrre le binary in `cli/dist/`, poi `scripts/build-npm.mjs` sincronizza le binary nei 6 sotto-pacchetti `@techreloaded/archetipo-{os}-{arch}` e le skill nel pacchetto principale `@techreloaded/archetipo`, infine `scripts/publish-npm.mjs` pubblica tutti i 7 pacchetti su npm.

## Installazione (per utenti finali)

Percorso principale (qualsiasi sistema con Node.js):

```bash
npm i -g @techreloaded/archetipo     # CLI globale nel PATH
archetipo init [--tool …] [--connector …]
```

Lo shim Node in `npm/archetipo/bin/archetipo.js` risolve il sub-package binario per la piattaforma corrente, setta `ARCHETIPO_DATA_DIR` e spawna la binary Go. Le skill bundle sono in `npm/archetipo/skills/` e vengono copiate da `archetipo init` verso `.{tool}/skills/` nel progetto.

Percorso alternativo (legacy, senza Node): gli installer `install.ps1` / `install.sh` continuano a funzionare e copiano la binary in `.archetipo/bin/archetipo` del progetto target.

## Note operative

- `.archetipo/config.yaml` in questo repo è un **template**: viene copiato nel progetto target dell'utente sotto `.archetipo/config.yaml`
- Il connector `file` è il default e usa file markdown locali. Il connector `github` richiede `gh` CLI autenticato
- Non esiste ancora un processo di test formale
