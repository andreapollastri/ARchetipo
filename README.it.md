<div align="center">

# ARchetipo

[English](README.md) · **Italiano**

**Un team AI al tuo fianco, dall'idea al prodotto finito.**

Un workflow spec driven che trasforma il tuo assistente AI in una squadra di sviluppo prodotto: analista, architetto, sviluppatore, tester, reviewer, designer, ognuno con il proprio ruolo e voce.

[![Status](https://img.shields.io/badge/status-beta-orange.svg)](#)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](#licenza)
[![Made for](https://img.shields.io/badge/made%20for-Claude%20Code%20%7C%20Codex%20%7C%20Gemini%20%7C%20OpenCode%20%7C%20Copilot-black.svg)](#)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](#)

[Quickstart](#quickstart) · [Skill](#skill) · [Come funziona](#come-funziona) · [Configurazione](#configurazione) · [FAQ](#faq)

</div>

---

## Perché ARchetipo

Gli AI coding agent sono potenti, ma tendono a rispondere a prompt isolati senza un processo. **ARchetipo introduce un flusso di lavoro ispirato ai team di prodotto reali**, con ruoli specializzati e artefatti persistenti (PRD, backlog, piani tecnici, mockup).

- **Un processo, non un prompt.** Dalla discovery all'implementazione, ogni fase ha la propria skill.
- **Agnostico rispetto al tool.** Le stesse skill funzionano su Claude Code, Codex, Gemini CLI, OpenCode e GitHub Copilot.
- **Autonomo quando serve.** Il flusso può essere guidato passo-passo oppure lanciato in autopilot sull'intero backlog.

---

## Quickstart

### 1. Installa la CLI una volta sola (globale)

```bash
npm i -g @techreloaded/archetipo
```

Un'unica installazione, funziona su macOS, Linux e Windows. Si aggiorna con `archetipo update`.

> Su Linux, se `npm i -g` lamenta problemi di permessi, imposta una volta
> un prefix nella tua home: `npm config set prefix ~/.npm-global` e
> aggiungi `~/.npm-global/bin` al `PATH`.

### 2. Inizializza un progetto

```bash
cd il-mio-progetto
archetipo init                                      # interattivo: scegli tool + connector
# oppure non-interattivo:
archetipo init --tool claude --connector file
```

`init` copia le skill di ARchetipo nella cartella del tool scelto
(`.claude/skills/`, `.gemini/skills/`, …) e scrive
`.archetipo/config.yaml` e `.archetipo/shared-runtime.md` nel progetto.

### Installer alternativi (senza Node.js)

Gli installer shell legacy sono ancora disponibili per ambienti senza npm:

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/techreloaded-ar/ARchetipo/main/install.sh | bash
# Windows (PowerShell)
irm https://raw.githubusercontent.com/techreloaded-ar/ARchetipo/main/install.ps1 | iex
```

Copiano la CLI in `.archetipo/bin/` del progetto. Per i nuovi progetti
preferisci il percorso npm sopra.

---

## Il workflow

ARchetipo è un set di **skill** che compongono un **worflow**. Ogni skill incarna una fase del processo, ispirata alla metodologia **Spec-Driven Development**: il ciclo `spec → plan → implement` si ripete per ogni incremento di prodotto in maniera continuativa.


```mermaid
flowchart LR
    I["<b>Inception</b><br/><i>→ PRD</i>"] --> S
    I -. initial mockup .-> D["<b>Design</b><br/><i>→ mockup</i>"]

    subgraph Loop["Spec-Driven Loop"]
        direction TB
        S["<b>Spec</b><br/><i>→ backlog</i>"] --> P["<b>Plan</b><br/><i>→ piano tecnico</i>"]
        P --> IM["<b>Implement</b><br/><i>→ codice + test</i>"]
        IM -. next story .-> S
    end

    P -. UI needed .-> D

    classDef oneshot fill:#eef,stroke:#557,stroke-width:1px,color:#000;
    classDef loop fill:#efe,stroke:#575,stroke-width:1px,color:#000;
    classDef aside fill:#fef,stroke:#755,stroke-width:1px,color:#000;
    class I oneshot;
    class S,P,IM loop;
    class D aside;
```

- **Inception** (`/archetipo-inception`) è one-shot: Lancia  la product discovery e produce un documento di requisiti `PRD` (visione, personas, MVP, architettura, requisiti funzionali).
- **Spec** (`/archetipo-spec`) apre il ciclo iterativo. Genera il `Backlog` iniziale partendo dal `PRD`, oppure lo estende con nuove user story.
- **Plan** (`/archetipo-plan`) pianifica la singola storia. Si occupa di analisi tecnca, task breakdown e strategia di test. Se la storia richiede UI nuova, invoca automaticamente Design.
- **Implement** (`/archetipo-implement`) esegue il piano. Genera codice e test, conduce una code review rigorosa e prepara la storia per essere revisionata dall'utente.
- **Design** produce mockup frontend distintivi. Viene invocato da `/archetipo-plan` quando serve UI per una nuova funzionalità, o direttamente con `/archetipo-design` per esplorare concept visivi senza toccare il codice applicativo.

Il ciclo `Spec → Plan → Implement` si ripete per ogni funzionalità.

### Il team

In ogni fase di ARchetipo, avrai a che fare con personas AI diverse, ognuna con ruolo e competenze ben definite.

| Persona | Ruolo | Competenza principale |
|---|---|---|
| 💎 **Andrea** | Product Manager | Vision, personas, scope dell'MVP |
| 🧭 **Costanza** | Business Strategist | Brainstorming e discovery |
| 🔎 **Emanuele** | Requirements Analyst | Chiarisce acceptance criteria ed edge case |
| 📐 **Leonardo** | Architect | Soluzione tecnica e decisioni architetturali |
| 🔧 **Ugo** | Full-Stack Developer | Implementazione e task breakdown |
| 🧪 **Mina** | Test Architect | Strategia di test e coverage |
| 🔍 **Cesare** | Code Reviewer | Qualità, sicurezza, aderenza al piano |
| ✨ **Livia** | UX Designer | Mockup e linguaggio visivo |

### Architettura connector

Le skill non sanno **dove** vivono gli artefatti: delegano la persistenza a un **connector** configurabile tramite la CLI `archetipo`.

- **Regole runtime** → `.archetipo/shared-runtime.md` (envelope JSON, gestione errori, disciplina CLI)
- **Operazioni** → comandi `archetipo ...` dichiarati esplicitamente dentro ogni skill

Cambiare connector avviene tramite `.archetipo/config.yaml`, senza toccare la logica di workflow delle skill.

| Connector | Dove finiscono gli artefatti |
|---|---|
| `file` *(default)* | File markdown locali nel repo |
| `github` | Issues + GitHub Projects v2 |
| *custom* | Linear, Jira, Notion, … (estendibile) |

---

## Dettagli delle Skill

| Skill | Scopo | Trigger tipici |
|---|---|---|
| **`archetipo-inception`** | Facilitazione interattiva della product discovery e generazione del PRD (visione, personas, MVP, architettura, requisiti funzionali). | "definisci il prodotto", "idea di prodotto", "scrivi un PRD" |
| **`archetipo-spec`** | Creazione del backlog iniziale dal PRD **oppure** aggiunta di nuove user story a un backlog esistente. Auto-rilevamento della modalità. | "crea il backlog", "aggiungi una storia", "serve una feature per…" |
| **`archetipo-design`** | Generazione di mockup frontend distintivi, isolati in `docs/mockups/`. Non tocca mai il codice applicativo. | "fammi un mockup", "concept della dashboard", "landing page" |
| **`archetipo-plan`** | Pianificazione tecnica di una user story: analisi, soluzione architetturale, task breakdown, strategia di test (con e2e quando serve). | "pianifica US-005", "come lo costruiamo?", "rompi la storia in task" |
| **`archetipo-implement`** | Implementazione guidata della storia pianificata: codice, test, esecuzione della suite, code review rigorosa, fix loop, transizione a `REVIEW`. | "implementa US-005", "esegui la prossima storia pronta" |

---

## Configurazione

Dopo l'installazione, `.archetipo/config.yaml` contiene i parametri del progetto:

```yaml
connector: file              # file | github

paths:
  prd: docs/PRD.md
  backlog: docs/BACKLOG.md   # solo per connector file
  planning: docs/planning/
  mockups: docs/mockups/
  test_results: docs/test-results/

workflow:
  statuses:
    todo: TODO
    planned: PLANNED
    in_progress: IN PROGRESS
    review: REVIEW
    done: DONE               # transizione manuale

github:                      # solo per connector github
  # owner: auto-detected
  # project_number: auto-detected
```

### Connector disponibili

#### `file` *(default)*

- Backlog in un singolo file markdown (`docs/BACKLOG.md`).
- Piani tecnici in `docs/planning/US-XXX.md`.
- Zero dipendenze esterne, tutto versionato con il tuo repo.

#### `github`

- Backlog come issue su un GitHub Project v2.
- Storie e task collegati tramite sub-issue.
- Transizioni di stato come campi custom del Project.
- Richiede `gh` CLI autenticato con permessi `repo` + `project`.

La CLI è l'unica superficie backend. Ogni skill documenta i sub-comandi `archetipo` che usa davvero.

---

## Filosofia

- **Output persistenti.** Ogni fase produce artefatti che vivono nel repo (o nel sistema connector). Il prossimo comando, o il prossimo giorno di lavoro,  parte da lì.
- **Autonomia responsabile.** Le skill si fermano solo davanti a blocker reali (dipendenze esterne, precondizioni CLI, ambiguità reali). Adattamenti locali, fix meccanici e aggiornamenti di test non richiedono conferma.
- **Tool-agnostico e connector-agnostico.** Cambiare AI agent o sistema di tracking non deve riscrivere il processo.

---

## FAQ

<details>
<summary><b>Devo usare tutte le skill?</b></summary>

No. Ogni skill è utilizzabile in isolamento. Puoi partire direttamente da `archetipo-plan` se hai già un backlog scritto a mano, o usare solo `archetipo-design` per esplorare concept visivi.
</details>

<details>
<summary><b>Posso usare ARchetipo su un progetto già esistente?</b></summary>

Sì. `archetipo-spec` aggiunge storie a un backlog esistente. `archetipo-plan` e `archetipo-implement` lavorano su qualsiasi storia presente, indipendentemente da come è stata creata.
</details>

<details>
<summary><b>Gli artefatti sono vincolati a un formato?</b></summary>

I template in `references/` sono modificabili. Se usi il connector `github`, le issue seguono un layout preciso, vedi `.archetipo/connectors/github.md`.
</details>

<details>
<summary><b>Perché i nomi italiani (Emanuele, Leonardo, Ugo, Mina, Cesare, Livia)?</b></summary>

ARchetipo nasce in Italia e mantiene una voce riconoscibile: un team con nomi propri funziona meglio di "the analyst agent" / "the architect agent". Le skill parlano comunque nella lingua della conversazione (auto-detected).
</details>

<details>
<summary><b>Come si fa il debug di una skill?</b></summary>

Ogni skill dichiara quali reference carica. Attiva la modalità verbose del tuo AI tool e controlla che i comandi `archetipo ...` attesi siano eseguiti nell'ordine corretto.
</details>

---

## Licenza

MIT © [techreloaded](https://github.com/techreloaded-ar)

---

<div align="center">

**Se ARchetipo ti è utile, lascia una ⭐ al repo e condividilo con il tuo team.**

[Report bug](https://github.com/techreloaded-ar/ARchetipo/issues) · [Richiedi feature](https://github.com/techreloaded-ar/ARchetipo/issues) · [Discussions](https://github.com/techreloaded-ar/ARchetipo/discussions)

</div>
