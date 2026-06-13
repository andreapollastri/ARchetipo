# match5 — Product Requirements Document

**Author:** Archetipo
**Date:** 2026-05-07
**Version:** 1.0

---

## Elevator Pitch

> Per **appassionati di calcetto** che hanno il problema di **non riuscire a trovare abbastanza giocatori per organizzare una partita**, **match5** è una **Progressive Web App** che **connette in pochi tap le persone disponibili a giocare nelle vicinanze**. A differenza di **WhatsApp e dei gruppi informali**, il nostro prodotto **offre un'esperienza radicalmente semplice, accessibile anche a chi non è a proprio agio con la tecnologia, trasformando ogni partita in un'opportunità per incontrare persone nuove**.

---

## Vision

match5 vuole diventare il punto di riferimento per chi vuole giocare a calcetto senza la fatica di organizzare: apri l'app, vedi chi gioca vicino a te, entri in campo. Niente gruppi WhatsApp da gestire, niente posti vuoti, niente partite saltate.

### Product Differentiator

UI radicalmente semplice e accessibile anche a utenti con bassa dimestichezza tecnologica — il mercato delle app sportive è progettato da e per ventenni iper-connessi; match5 serve anche il cinquantenne appassionato che vuole solo cliccare "gioco giovedì alle 21". In più, ogni partita è un'occasione di socializzazione reale: match5 non è solo logistica, è uno strumento per incontrare persone nuove attraverso lo sport.

---

## User Personas

### Persona 1: Marco

**Ruolo:** Impiegato, ex-giocatore amatoriale
**Età:** 52 | **Background:** Giocava ogni settimana con un gruppo fisso di amici. Il gruppo si è sfaldato negli anni (figli, lavoro, traslochi). Vuole tornare a giocare ma non sa da dove ricominciare.

**Goals:**
- Tornare a giocare a calcetto regolarmente
- Incontrare persone con la stessa passione
- Riempire il giovedì sera con qualcosa di concreto

**Pain Points:**
- Non sa usare app complesse
- Ha paura di essere l'unico a non conoscere nessuno in campo
- Teme che il livello di gioco degli altri sia troppo diverso dal suo

**Behaviors & Tools:**
Usa WhatsApp e Facebook quotidianamente. Poca dimestichezza con app nuove. Preferisce flussi a pochi passaggi, testo grande, niente elementi superflui.

**Motivazioni:** Ritrovare una dimensione sociale che ha perso con lo sfaldamento del gruppo storico.
**Tech Savviness:** Basso-medio

#### Customer Journey — Marco

| Fase | Azione | Pensiero | Emozione | Opportunità |
|---|---|---|---|---|
| Awareness | Vede un post sui social o un volantino al centro sportivo | "Esiste un'app per trovare gente con cui giocare?" | Curiosità mista a scetticismo | Comunicazione semplice: "Un tap e sei in campo" |
| Consideration | Apre il link, vede la schermata di accesso | "Spero non richieda mille passaggi per registrarsi" | Apprensione | Login Google a un tap, zero form da compilare |
| Primo uso | Cerca partite vicino a casa, trova una con "Manca 1" | "Non conosco nessuno ma ci provo" | Nervosismo e curiosità | Badge affidabilità dei partecipanti per rassicurarlo |
| Uso regolare | Entra in 2-3 partite, inizia a riconoscere i volti | "Questi li ho già visti la settimana scorsa" | Appartenenza | Notifica "Giochi anche giovedì?" per rafforzare l'abitudine |
| Advocacy | Consiglia l'app a un ex-compagno di squadra | "Finalmente qualcosa che funziona anche per noi" | Soddisfazione | Meccanismo di invito semplice (link diretto) |

---

### Persona 2: Sara

**Ruolo:** Professionista, trasferita in una nuova città per lavoro
**Età:** 28 | **Background:** Ha sempre giocato a calcetto con colleghi e amici. Si è trasferita e non conosce ancora nessuno. Vuole ricominciare a giocare e costruire una rete sociale.

**Goals:**
- Trovare partite miste vicino a casa
- Conoscere persone al di fuori del contesto lavorativo
- Giocare con continuità almeno una volta a settimana

**Pain Points:**
- Non sa come trovare gruppi locali di calcetto
- Teme di non essere accettata in gruppi già formati e chiusi
- Vuole capire il livello di gioco prima di iscriversi per evitare situazioni imbarazzanti

**Behaviors & Tools:**
Iper-connessa: usa Instagram, Meetup, app di fitness. Si fida delle recensioni e dei profili altrui. Si aspetta un'esperienza fluida e moderna.

**Motivazioni:** Usare lo sport come leva per costruire una rete sociale nella nuova città.
**Tech Savviness:** Alto

#### Customer Journey — Sara

| Fase | Azione | Pensiero | Emozione | Opportunità |
|---|---|---|---|---|
| Awareness | Trova match5 cercando "calcetto Milano" su Google | "Vediamo se c'è qualcosa di meglio dei soliti gruppi Facebook" | Speranza | SEO locale + recensioni visibili anche senza login |
| Consideration | Naviga le partite disponibili, legge i profili dei partecipanti | "Il livello sembra giusto, l'indicatore di affidabilità mi tranquillizza" | Fiducia crescente | Profili dettagliati con livello e storico affidabilità |
| Primo uso | Invia richiesta di partecipazione, viene accettata, riceve notifica | "Ok, domani gioco — speriamo bene" | Eccitazione mista ad ansia | Reminder automatico 2h prima con dettagli partita |
| Uso regolare | Partecipa a 1-2 partite a settimana, riconosce i giocatori abituali | "Questo è diventato il mio gruppo" | Appartenenza e soddisfazione | Partite ricorrenti (feature Growth) |
| Advocacy | Invita una collega a unirsi | "È l'app più semplice che abbia trovato per questo" | Orgoglio | Meccanismo di invito con link diretto alla partita |

---

## Brainstorming Insights

> Scoperte chiave e direzioni alternative esplorate durante la sessione di inception.

### Assumptions Challenged

- **"Le persone vogliono giocare con amici"** — sfidato: esiste un segmento significativo di persone sole in città o con il gruppo storico sfaldato che cerca attivamente di giocare con sconosciuti. match5 serve esattamente questo segmento ignorato.
- **"Un'app sportiva deve competere con Meta/WhatsApp sulle feature"** — sfidato: il vantaggio competitivo non è nella ricchezza di funzionalità ma nella semplicità radicale per utenti non tech-native.
- **"Scala nazionale dal giorno 1"** — sfidato: la densità di utenti in un'area geografica piccola vale più della copertura nazionale dispersa. Lancio hyper-local è la scelta giusta.

### New Directions Discovered

- **match5 come strumento di socializzazione**, non solo di logistica sportiva: il prodotto può posizionarsi come "il modo più semplice per incontrare persone nuove attraverso il calcetto".
- **Partnership con centri sportivi** come canale di acquisizione: i campi hanno slot vuoti da riempire, match5 porta giocatori — win-win naturale per il go-to-market.
- **Seeding su gruppi WhatsApp esistenti**: il lancio non parte da zero ma dalla migrazione di organizzatori già attivi su chat informali.

---

## Product Scope

### MVP — Minimum Viable Product

1. Accesso tramite OAuth Google (zero form, un tap)
2. Profilo giocatore: livello, foto, bio, indicatore affidabilità
3. Creazione evento partita: data, ora, luogo (mappa), max giocatori, livello
4. Mappa + lista partite nelle vicinanze con geolocalizzazione
5. Filtro partite imminenti + badge "Manca 1 giocatore"
6. Richiesta di partecipazione + accettazione/rifiuto da parte dell'organizzatore
7. Notifiche push: richiesta accettata/rifiutata, partita al completo, partita cancellata, reminder 2h prima
8. Conferma presenze post-partita + aggiornamento automatico affidabilità
9. Cancellazione/modifica evento (solo organizzatore)

### Growth Features (Post-MVP)

- Integrazione prenotazione campi sportivi (partnership con centri)
- Sistema di rating post-partita tra giocatori
- Partite ricorrenti ("ogni giovedì alle 21")
- Chat interna all'evento
- Statistiche personali (partite giocate, gol, ecc.)
- Modello freemium (feature premium a pagamento per organizzatori)

### Vision (Future)

- Tornei amatoriali organizzati tramite app
- Classifica giocatori per zona geografica
- Partnership con brand sportivi / sponsor locali
- Espansione ad altri sport (basket, padel, beach volley)

---

## Technical Architecture

> **Proposta da:** Leonardo (Architect)

### System Architecture

match5 è una **Progressive Web App (PWA)** costruita sul boilerplate Next.js 15 esistente. L'approccio PWA permette di offrire un'esperienza mobile nativa (installabile su iOS e Android, notifiche push, accesso geolocalizzazione) senza mantenere codebase separate. Per il target Marco, "apri il link e installa" è più accessibile dello store; per Sara è irrilevante.

**Architectural Pattern:** Modular Monolith (Next.js App Router) con servizi managed (Supabase, Vercel)

**Main Components:**
- **Frontend:** Next.js App Router, mobile-first, shadcn/ui + Tailwind v4
- **API Layer:** Next.js Route Handlers (REST)
- **Database:** Supabase PostgreSQL con estensione PostGIS per query geografiche
- **Real-time:** Supabase Realtime per aggiornamenti live (slot disponibili, partita al completo)
- **Auth:** Supabase Auth con OAuth Google (già implementato nel boilerplate)
- **Mappe:** Mapbox GL JS per visualizzazione partite su mappa
- **PWA:** next-pwa per service worker, manifest e installabilità
- **Push Notifications:** Web Push API + Supabase Edge Functions

### Technology Stack

| Layer | Tecnologia | Versione | Rationale |
|---|---|---|---|
| Framework | Next.js (App Router) | 15.x | Boilerplate esistente — SSR, routing, API routes |
| Language | TypeScript | 5.x | Boilerplate esistente |
| UI Components | shadcn/ui | latest | Boilerplate esistente |
| CSS | Tailwind CSS | v4 | Boilerplate esistente |
| Auth | Supabase Auth (OAuth Google) | managed | Boilerplate esistente — login a un tap |
| Database | Supabase PostgreSQL + PostGIS | 16.x | Boilerplate esistente + PostGIS per query geografiche |
| ORM | Prisma | 5.x | Boilerplate esistente |
| Real-time | Supabase Realtime | managed | Aggiornamenti live senza polling |
| Mappe | Mapbox GL JS | 3.x | Leggero, mobile-friendly, ottimo SDK React |
| PWA | next-pwa | 5.x | Service worker e installabilità |
| Push Notifications | Web Push API + Supabase Edge Functions | — | Notifiche native su mobile |
| Storage | Supabase Storage | managed | Foto profilo giocatori (boilerplate esistente) |
| Deployment | Vercel | — | Zero config con Next.js 15 |

### Project Structure

**Organizational pattern:** Feature-based, estensione del boilerplate esistente

```
src/
  app/
    matches/
      page.tsx                  # Lista + mappa partite vicine
      [id]/
        page.tsx                # Dettaglio partita
      create/
        page.tsx                # Crea evento partita
    profile/
      page.tsx                  # Profilo giocatore
    api/
      matches/
        route.ts                # CRUD partite
        nearby/route.ts         # Query geospaziale PostGIS
      participations/
        route.ts                # Gestione richieste partecipazione
      notifications/
        route.ts                # Trigger Web Push
      post-match/
        route.ts                # Conferma presenze
  components/
    matches/                    # Card partita, badge "Manca 1", filtri
    map/                        # Componente mappa Mapbox
    profile/                    # Scheda giocatore, indicatore affidabilità
  lib/
    postgis.ts                  # Helper query geografiche
    push.ts                     # Helper Web Push
    supabase/                   # Già in boilerplate
    prisma.ts                   # Già in boilerplate
prisma/
  schema.prisma                 # + Match, Participation, PlayerProfile
public/
  manifest.json                 # PWA manifest
  sw.js                         # Service worker (generato da next-pwa)
```

**Estensione schema Prisma:**
```prisma
enum Level {
  BEGINNER
  INTERMEDIATE
  ADVANCED
}

enum MatchStatus {
  OPEN
  FULL
  CANCELLED
  COMPLETED
}

enum ParticipationStatus {
  PENDING
  ACCEPTED
  DECLINED
}

model Match {
  id            String         @id @default(uuid())
  createdBy     String
  location      String
  lat           Float
  lng           Float
  scheduledAt   DateTime
  maxPlayers    Int
  level         Level
  status        MatchStatus    @default(OPEN)
  participants  Participation[]
  createdAt     DateTime       @default(now())
}

model Participation {
  id        String              @id @default(uuid())
  matchId   String
  userId    String
  status    ParticipationStatus @default(PENDING)
  showedUp  Boolean?
  match     Match               @relation(fields: [matchId], references: [id])
}

model PlayerProfile {
  userId      String  @id
  level       Level
  reliability Float   @default(100)
  bio         String?
}
```

### Development Environment

Ambiente locale basato sul boilerplate esistente (Node.js, npm, Turbopack dev server).

**Required tools:** Node.js 20+, npm, Supabase CLI (per PostGIS migration), Mapbox API key

### CI/CD & Deployment

**Build tool:** Turbopack (dev) / Next.js build (prod)

**Pipeline:** GitHub Actions → type-check + build → deploy automatico

**Deployment:** Vercel (zero config)

**Target infrastructure:** Vercel (frontend + API) + Supabase (database, auth, storage, real-time, edge functions)

### Architecture Decision Records (ADR)

| Decisione | Scelta | Alternativa scartata | Motivazione |
|---|---|---|---|
| App distribution | PWA | React Native / Expo | Evita doppia codebase; accessibilità immediata senza store; target Marco non usa store |
| Query geografiche | PostGIS | Calcolo distanza in JS | Performance e scalabilità: query geospaziali a livello DB, non in memoria |
| Real-time | Supabase Realtime | Polling / WebSocket custom | Già incluso nello stack; zero infrastruttura aggiuntiva |
| Mappe | Mapbox GL JS | Leaflet + OpenStreetMap | SDK React più maturo, performance mobile migliore; costo accettabile per volumi MVP |
| Auth | Google OAuth only | Email/password | Riduce attrito per Marco (zero password da ricordare); Google è l'unico OAuth supportato dal boilerplate |

---

## Functional Requirements

### Area 1 — Profilo Giocatore
*Estende boilerplate: User model + Supabase Auth*

**FR1** — Il giocatore può completare il proprio profilo specificando: livello di gioco (Principiante / Intermedio / Avanzato), foto profilo e bio breve.

**FR2** — Il sistema calcola e visualizza un **indicatore di affidabilità** (0–100%) per ogni giocatore, basato sulla percentuale di partite confermate a cui si è effettivamente presentato.

### Area 2 — Creazione e Gestione Partita

**FR3** — L'organizzatore può creare un evento partita specificando: data, ora, luogo (selezionabile su mappa Mapbox), numero massimo di giocatori (da 4 a 12), livello richiesto.

**FR4** — L'organizzatore può modificare i dettagli di una partita o cancellarla; in caso di cancellazione tutti i partecipanti accettati ricevono notifica automatica.

### Area 3 — Scoperta Partite

**FR5** — Il giocatore visualizza una **mappa interattiva** (Mapbox) e una **lista** delle partite disponibili entro un raggio configurabile dalla propria posizione, rilevata tramite Geolocation API del browser.

**FR6** — La lista mostra in cima le partite **più imminenti**; le partite con un solo posto libero mostrano il badge visivo **"Manca 1"**.

**FR7** — Il giocatore può filtrare le partite per: livello di gioco, data/ora, distanza massima.

**FR8** — Il giocatore può accedere al **dettaglio di una partita**: informazioni generali, mappa del luogo, lista dei partecipanti accettati con profilo (livello, affidabilità).

### Area 4 — Partecipazione

**FR9** — Il giocatore può inviare una **richiesta di partecipazione** a una partita con status OPEN; non può inviare richieste duplicate alla stessa partita.

**FR10** — L'organizzatore riceve le richieste e può **accettare o rifiutare** ogni giocatore; quando si raggiunge il numero massimo, la partita passa automaticamente a status FULL.

**FR11** — Un giocatore accettato può **abbandonare** una partita; l'organizzatore riceve notifica immediata e la partita torna a status OPEN se era FULL.

### Area 5 — Notifiche Push

**FR12** — Il giocatore riceve una notifica push quando la propria richiesta viene **accettata o rifiutata** dall'organizzatore.

**FR13** — Tutti i partecipanti ricevono una notifica push quando la partita raggiunge il **numero massimo di giocatori** (status FULL).

**FR14** — Tutti i partecipanti ricevono una notifica push se la partita viene **cancellata** dall'organizzatore.

**FR15** — I partecipanti accettati ricevono un **reminder push 2 ore prima** dell'inizio della partita.

### Area 6 — Post-Partita

**FR16** — A partita conclusa, l'organizzatore può **marcare quali giocatori si sono effettivamente presentati** tramite una schermata dedicata.

**FR17** — Il sistema **aggiorna automaticamente l'indicatore di affidabilità** di ogni giocatore in base alle presenze registrate dall'organizzatore.

---

## Non-Functional Requirements

### Security

- Tutti i dati di geolocalizzazione sono trasmessi esclusivamente via HTTPS.
- La posizione esatta dei giocatori non è mai esposta pubblicamente: nella lista partite è visibile solo la **distanza approssimativa** (es. "a 800m da te"), non le coordinate precise.
- L'autenticazione è delegata interamente a Supabase Auth (OAuth Google) — nessuna password gestita dall'applicazione.

### Accessibilità & UX

- UI mobile-first con dimensione minima del testo di 16px e contrasto elevato (WCAG AA).
- Ogni azione principale (trovare una partita, iscriversi, creare un evento) deve essere completabile in **massimo 3 tap** dalla home.
- Nessuna terminologia tecnica nell'interfaccia: linguaggio semplice e diretto.

### Integrations

- **Mapbox GL JS** per visualizzazione mappe e geocoding.
- **Supabase PostGIS** per query geospaziali (trova partite entro X km).
- **Supabase Realtime** per aggiornamenti live degli slot disponibili.
- **Web Push API + Supabase Edge Functions** per notifiche push su mobile.

---

## Next Steps

1. **UX Design** — Definire flussi di interazione dettagliati e wireframe per le feature MVP (priorità: home map view, create match flow, participation flow)
2. **Database** — Abilitare estensione PostGIS su Supabase e aggiornare lo schema Prisma con i nuovi modelli
3. **Backlog** — Decomporre i requisiti funzionali in epiche e user story con `/archetipo-spec`
4. **Validazione** — Test di usabilità con almeno un utente del profilo Marco prima di procedere con lo sviluppo

---

_PRD generato via Archetipo Product Inception — 2026-05-07_
_Sessione condotta da: Stefano Leli con il team Archetipo_
