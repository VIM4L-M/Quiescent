# quiescent

![quiescent — a durable retry sequencer for failed mandate debits](image/image.png)

> A durable retry sequencer for failed mandate debits. Spends a scarce,
> regulator-capped attempt budget optimally — and never debits twice, even
> across process crashes and multi-day sequences.

**On uncertainty, stop.**

---

## The problem

A customer authorises a mandate — UPI Autopay or eNACH — and the merchant
pulls money automatically each cycle. A meaningful share of these debits
fail: temporary balance gaps, banks briefly unreachable, a technical decline
that has nothing to do with the customer. Retried carelessly, that failure
becomes a revoked mandate and a lost customer.

Between *debit failed* and *customer churned* sits recoverable money. Closing
that gap is not a retry loop. It is a scheduling problem under hard
constraints, executed durably over days:

- **4 attempts per cycle.** Regulator-capped, non-renewable. Every attempt
  spent badly is 25% of that cycle's recovery capacity gone, permanently.
- **Blocked execution windows.** Two daily windows where autopay isn't
  allowed to fire at all. Firing into one returns a technical decline —
  **and still consumes an attempt.**
- **24-hour pre-debit notification.** Every attempt — original *and* every
  retry — needs its own real warning sent at least a day ahead. No warning,
  no debit.
- **Per-rail asymmetry.** On eNACH, a failed debit can incur a bounce charge
  levied on the *customer*. Retrying blindly doesn't just fail — it charges
  someone for the privilege.

**The goal is not maximum recovery at any cost.** It's correct disposition:
every cycle ends recovered, escalated, or cleanly abandoned, for a defensible
reason, within budget, without harming the customer along the way.

---

## What's actually different here

Most recovery systems model the *decision* — which code to retry, when —
and stub out the *execution*, trusting that "send the retry" is the easy
part. It isn't. The hard, unglamorous part is making that decision happen
**exactly once**, across days, across process crashes, with three worker
processes racing to fire the same cycle.

Everything here derives from one fact:

> Exactly-once delivery across a network does not exist. You get
> at-least-once with an idempotent receiver, or at-most-once with a
> reconciliation pass. This system chooses reconciliation for money
> movement — which is why `unknown` is a first-class state, not an error.

So the decision logic is deliberately simple: a deterministic classification
table plus a constraint solver, no machine learning on the money path. The
engineering effort goes almost entirely into the part that's actually hard —
proving that decision survives a crash mid-debit, a stalled worker, two
processes racing for the same budget slot, without ever double-charging or
silently burning a retry attempt for nothing.

Two real bugs found this way, on this exact project, are documented start to
finish in [`docs/JOURNAL.md`](docs/JOURNAL.md) — what we believed, what
actually happened, why, and the test that now proves it can't happen again.

---

## Architecture

One Go binary. Seven subcommands that run once and exit
(`migrate`/`seed`/`create`/`cycle`/`policy`/`report`/`verify`), four that run
forever as independent processes:

```mermaid
flowchart LR
    SCHED["scheduler ×1<br/>decides &amp; reserves budget<br/>never fires a debit"]
    WORK["worker ×3<br/>fires debits, sends notices<br/>never decides anything"]
    AI["intelligence ×1<br/>classifies, narrates, proposes<br/>cannot write cycle state"]
    BANK["provider-sim ×1<br/>simulated bank<br/>deliberately hostile"]
    DB[("PostgreSQL<br/>the only shared state")]

    SCHED -- "reserve budget, plan a time" --> DB
    WORK -- "claim lease, fire, record outcome" --> DB
    WORK -- "Debit / Status, fenced" --> BANK
    AI -- "reads the audit trail" --> DB
```

Every boundary above exists because a specific failure mode required it —
none exist by convention. `scheduler`/`worker` are split because scheduling
is idempotent and execution isn't; three `worker` processes exist because a
lease-expiry race is *unreachable* with only one. `intelligence` holds no
database handle and no executor — not by policy, but because those types
simply aren't in its scope, so writing cycle state from there **fails to
compile**. Full reasoning: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

### The states a cycle moves through

```mermaid
stateDiagram-v2
    [*] --> pending
    pending --> scheduled: scheduler reserves an attempt
    scheduled --> in_flight: worker claims the lease and fires
    in_flight --> recovered: bank confirms SUCCESS
    in_flight --> pending: retryable failure, budget refunded
    in_flight --> escalated: hard decline or terminal code
    in_flight --> unknown: crash or timeout, outcome not known
    unknown --> recovered: reconciliation confirms it was debited
    unknown --> pending: reconciliation confirms it wasn't
    unknown --> held: still unknown after repeated checks
    pending --> abandoned: budget exhausted, never recovered
    recovered --> [*]
    escalated --> [*]
    abandoned --> [*]
    held --> [*]
```

`unknown` and `held` exist so genuine uncertainty has somewhere safe to live,
instead of being forced into a guess. Nothing ever transitions back from
`unknown` to `in_flight` — that path is exactly how a real system
double-debits a customer.

---

## Correctness

Six invariants, checked after every run against the live database. Each is
a query that must return **zero rows**, and each one has a paired negative
test — a seeded row that violates it, proving the check actually catches
something rather than being green because nothing ever tries it:

1. No cycle debited more than once
2. `attempts_used` never exceeds 4
3. Every cycle reaches a terminal disposition, or is explicitly held
4. Budget agrees with the fired attempts that actually exist
5. No attempt fired into a blocked execution window
6. No attempt fired without a real notice delivered 24h ahead

```
$ quiescent verify
  [PASS] 1. No cycle debited more than once
  [PASS] 2. attempts_used never exceeds 4
  [PASS] 3. Every cycle reaches a terminal disposition (or is held)
  [PASS] 4. Budget agrees with fired attempts
  [PASS] 5. No attempt fired into a blocked execution window
  [PASS] 6. No attempt fired without a notice delivered 24h ahead
6/6 passed
```

The full failure taxonomy this project is built against — 29 named failure
modes across five categories, crash points through policy violations to
recovery-harm — lives in [`docs/FAILURES.md`](docs/FAILURES.md). Which of
those were actually witnessed live, and how each was found and fixed, is in
[`docs/JOURNAL.md`](docs/JOURNAL.md).

---

## Where AI sits

Three jobs, **none of them on the money path**:

1. **Classifying unmapped failure codes** the deterministic table doesn't
   cover, with a confidence score. Below threshold → human queue, never a
   guess acted on directly.
2. **Proposing policy changes** from batch outcomes, for a human to approve.
   The AI writes rules; it never executes them.
3. **Narrating audit trails** into plain English, for a human reading the
   history of one cycle.

The `intelligence` package receives exactly one interface, returning a
**value type** — no store, no executor, no provider client. It cannot write
cycle state, reserve an attempt, or move money, not by policy but because
those types are never in scope. Every call is bounded: a short timeout, a
circuit breaker, capped concurrency, a defined fallback — if the answer is
ever "we couldn't proceed because the AI was slow," the design is wrong.

---

## Measured

Reported against two comparators, not a raw recovery rate: a naive fixed
24h/72h/7d baseline with no classification or prediction, and an oracle
that reads true success probabilities directly — not deployable, just the
theoretical ceiling.

```
system    61.60%
baseline  61.62%
oracle    63.99%

achievable lift   2.37 pp
captured lift     ≈ -0.02 pp (95% CI)
```

Stated plainly: **this system does not currently beat the naive baseline.**
That's a real, measured result, not a bug — `solve` fixes the retry *day* to
the conservative regulatory reading (constraint box, `CLAUDE.md`), which
makes its days identical to the baseline's by construction. The oracle's
2.37 percentage points of proven achievable lift are only reachable by
choosing a *different* day, which the current conservative stance
deliberately forbids. Closing that gap is the clearest next lever, and it's
an open, honestly-labeled question — not a hidden one.

---

## Quickstart

```bash
docker-compose up -d                 # Postgres
go build -o quiescent.exe ./cmd/quiescent

$env:DB_URL = "postgres://quiescent:quiescent@localhost:5433/quiescent?sslmode=disable"
.\quiescent.exe migrate
.\quiescent.exe verify               # 6/6 on an empty database

# four long-running processes, each in its own terminal, same DB_URL set:
.\quiescent.exe provider-sim
.\quiescent.exe scheduler
.\quiescent.exe worker
.\quiescent.exe intelligence

# back in your first terminal — create something to watch happen:
.\quiescent.exe create --rail upi_autopay --amount 50000 --fire-in 30s
.\quiescent.exe cycle <the-cycle-id-just-printed>

# batch measurement, no live processes needed:
.\quiescent.exe seed --customers 50
.\quiescent.exe policy
.\quiescent.exe report
```

`quiescent --help` and `quiescent <command> --help` describe every flag.

---

## Docs

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — roles, boundaries,
  mechanisms, data model, in full
- [`docs/FAILURES.md`](docs/FAILURES.md) — the 29-item failure taxonomy this
  project is built against
- [`docs/JOURNAL.md`](docs/JOURNAL.md) — what broke, what we did, as it
  happened — read this first

---

## Stack

Go, PostgreSQL, Docker Compose. Four dependencies, each earning its place:

```
jackc/pgx/v5                  Postgres
golang-migrate/migrate        migrations, used as a library
golang.org/x/time/rate        token bucket for the worker claim loop
spf13/cobra                   CLI subcommand dispatch
```

No LLM SDK — `intelligence/` calls GroqCloud's chat completions API over
plain `net/http`, the same pattern the bank simulator's client already uses.
No web framework — Go's own routing is enough. No ORM — the SQL *is* the
correctness, and it has to stay readable. **No workflow engine** — durable
execution across a crash is the thing this project demonstrates, not a
problem to outsource to a dependency.

---

## Limitations

Stated plainly, because they're real:

- **The provider is a simulator.** Outcomes follow a modelled distribution,
  not measured production data. The comparison *between* policies (system
  vs. baseline vs. oracle) is meaningful; the absolute recovery percentage
  is not a production forecast.
- **Regulatory constants are sourced, not independently verified against
  the primary regulator text in every case.** Where a reading is a working
  assumption rather than a confirmed fact, it's labeled as such in the code
  and the docs, not quietly assumed.
- **No live Razorpay integration.** Test-mode payment APIs can't produce a
  genuine timeout-after-commit or a stale-fence rejection, both essential to
  the crash-safety demonstration — which is exactly why the simulator is
  deliberately hostile rather than lenient.
- **Single-node Postgres.** Failover to a lagging replica could in principle
  affect lease visibility. Out of scope for this submission.
- **The prediction layer is arithmetic, not a trained model.** On a
  synthetic batch this size, a trained model would be both worse and harder
  to defend than a simple, explainable rule.
