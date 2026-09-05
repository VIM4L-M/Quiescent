# Journal

What broke, what we did, as it happened.

This is the primary submission artefact. The buildathon reads *"what broke and
how did you get out of it"* first. Written at the time, not reconstructed at
the end.

**Entry format.** Every entry answers four questions:

1. What did I believe?
2. What happened instead?
3. Why — the actual mechanism, not the symptom?
4. What changed, and what test now catches it?

An entry without a test reference is incomplete.

---

## 2026-08-25 · Two schema bugs found before writing any code

**Believed:** the data model was sound after the first architecture pass.

**Happened:** walking through the lease acquisition SQL line by line, two
separate defects surfaced.

**Why — bug 1, budget desync.** The budget increment and the attempt insert
were written as separate statements. A crash between them permanently consumes
25% of a cycle's budget with no attempt row recording why. Nothing errors, no
exception is raised, no log line appears. The number is silently wrong forever.
Only a count comparison would ever detect it.

**Why — bug 2, fence held without a lease.** The fence lived in two places:
`mandate_cycles` and `leases`. Acquisition incremented the `mandate_cycles`
counter unconditionally, *then* attempted the lease insert with an expiry
guard. If the lease was held by a live worker, step two failed — but step one
had already handed the caller a fresh, valid-looking token. A worker could walk
away with fence 8 while not holding the lease, and the provider would accept it
as newer than the legitimate holder's 7.

**Changed:**

- Budget CAS and attempt insert (and now the outbox notice row) share one
  transaction. All three commit or none do.
- `fence` deleted from `mandate_cycles` entirely. The lease row is the single
  source of truth. Acquisition is now one statement whose `WHERE` guard and
  fence increment cannot be separated.

The second fix is the more instructive one. The bug was not fixed by adding a
validation — it was fixed by **removing the duplicated state that made the bug
expressible**. Two copies of one fact was the root cause; a guard would have
been a patch over it.

A free consequence: because expiry is now evaluated as `expires_at < now()`
inside a single Postgres statement, every worker measures against the same
clock. Cross-machine clock skew is no longer mitigated — it is structurally
impossible.

Bug 1 is **C7** in `docs/FAILURES.md` — budget spent with no attempt row to
show for it.

**Tests:** `TestReserveAttemptSucceedsAndAdvancesBudget` and
`TestC7ReserveAttemptLeavesNothingBehindOnFailure` (bug 1 — the transaction
that makes C7 impossible), `TestAcquireFailsWhenAlreadyHeld` (bug 2 — the
single-statement acquisition). This entry originally cited
`TestBudgetAndAttemptCommitTogether` and `TestFenceNotIssuedWhenLeaseHeld` —
checked against `git log -S` across the whole history on 2026-09-04, and
neither test ever existed under either name, at any commit. They were named
here before being written and never actually built; different, real tests
covering the same mechanism were built instead, and this entry was never
reconciled against them until now. Invariant 4 in `quiescent verify` catches
the first class in any batch run.

---

## 2026-08-25 · Reconciliation was unguarded

**Believed:** reconciliation was safe because the provider status query is a
read, so no double-debit was possible.

**Happened:** correct about the debit, wrong about the outcome. All three
workers poll for `unknown` cycles and all three find the same one. All three
run the query, all three receive "not debited," and all three attempt to move
the cycle to `pending`. The version-guarded CAS saves it only if they happen to
read different versions.

**Why:** an operation that transitions cycle state was not treated like one.
The rule had been "debits take the lease," which was too narrow.

**Changed:** the rule is now uniform — **any operation that transitions cycle
state acquires the lease first.** Reconciliation uses a 5-minute lease rather
than 30 seconds, since backoff-and-retry on the status query can outlast the
shorter duration. Renewal would require a heartbeat mechanism the system does
not otherwise need, and the cost of a stuck reconciliation lease is latency,
not correctness.

No new machinery — the same lease table, the same statement, the same fence.
Closes **C6**.

**Test:** cited here as `TestConcurrentReconciliationSerialises` — also never
existed at any commit, per the same `git log -S` audit noted in the entry
above. The real coverage is `TestC6ResolveSkipsWhenLeaseHeld`: not literal
concurrent goroutines, but a pre-held lease from another holder, asserting
`Resolve` returns `ResultNotMyTurn` rather than racing. A true
concurrent-goroutines version of this test does not exist yet — flagged
rather than quietly left as a citation to something written.

---

## 2026-08-25 · Stale attempts must be marked, never deleted

**Believed:** an overdue attempt returned to the scheduler could simply have
its row deleted and be rescheduled cleanly.

**Happened:** invariant 4 (`attempts_used` equals attempt row count) fires
immediately. `attempts` is append-only; deleting from it breaks the audit
trail *and* the consistency check simultaneously.

**Why:** the attempt was conflated with the *budget it consumed*. Those are
separate facts. An attempt that was never fired consumed no budget, but the row
is still the record that a decision was made and then superseded.

**Changed:** mark `outcome = 'ABANDONED_STALE'`, decrement `attempts_used` in
the same transaction, and exclude stale rows from invariant 4's count.

**Guarded on `fired_at IS NULL`.** Budget is only refundable if the request
never left the process. If it was sent, the outcome is unknown by definition —
that is an `unknown`, not a stale attempt, and refunding budget there would be
a correctness hole.

**Test:** cited here as `TestStaleAttemptRefundsBudgetOnlyWhenUnfired` — same
audit, same result: never existed under that name. Real coverage is
`TestE2AbandonedStaleOnlyOnAnUnfiredAttempt`, which asserts exactly this —
`ABANDONED_STALE` succeeds pre-fire and is rejected with `ErrConflict`
post-fire.

---

## 2026-08-25 · The state machine on paper is not the state machine in code

**Believed:** forbidding `unknown → in_flight` in the state machine prevented
retrying out of uncertainty.

**Happened:** it prevents the *worker* from retrying. It does nothing to stop
the **scheduler** from picking up a cycle sitting in `unknown` and scheduling a
fresh attempt while reconciliation is still pending. A double-debit through the
front door, with the state machine fully intact.

**Why:** a diagram describes intent. A query enforces it. The scheduler's claim
query had no state filter.

**Changed:** `WHERE state NOT IN ('unknown', 'held')` in the scheduler's claim
query.

**Test:** `TestSchedulerSkipsUnknownAndHeld`

---

## 2026-08-25 · Invariant 5 was green for the wrong reason

**Believed:** the six invariant queries *are* the correctness claim. Zero rows
back means the system behaved.

**Happened:** invariant 5 returns zero rows against a database holding an
attempt fired squarely inside the blocked window.

```
 old_form_catches | new_form_catches
------------------+------------------
                0 |                1
```

One attempt, `fired_at = 2026-03-08 11:00:00+05:30` — 11:00 IST, an hour into
the 10:00–13:00 no-autopay window. The original query does not see it.

**Why:** `fired_at` is `timestamptz`. A bare `::time` cast renders it in
whatever the *session's* `TimeZone` happens to be, and the container runs UTC.
So `fired_at::time BETWEEN '10:00' AND '13:00'` was interrogating 10:00–13:00
**UTC** — 15:30–18:30 IST, a window `solve` deliberately schedules *into*. The
real blocked window sits at 04:30–07:30 UTC, which the query never looked at.

This was not a loose check. It was a precise check of the wrong window, and it
would have returned zero rows under every injection point, on every run, for
the life of the project.

**Changed:** the invariant carries its own zone instead of inheriting one.

```sql
WHERE (fired_at AT TIME ZONE 'Asia/Kolkata')::time BETWEEN '10:00' AND '13:00'
```

The alternative — `ALTER DATABASE … SET timezone TO 'Asia/Kolkata'` — was
rejected. It makes correctness depend on a session GUC that any `psql` can
override, which is invisible state: the same class of problem as the duplicated
fence, and the same fix applies. Better that the database stay UTC and the query
say what it means. `TZ`/`PGTZ` are pinned to UTC in `docker-compose.yml` so the
setting is explicit rather than inherited from a host.

**Test:** the reproduction above, run against 000001 before any application code
existed. Becomes a case in the invariant suite — seed one attempt at 11:00 IST,
assert invariant 5 returns exactly one row.

---

## 2026-08-25 · Invariant 6 was blind to the worst case it existed to catch

**Believed:** invariant 6 — "no attempt fired without a delivered notice" —
was written down twice, in CLAUDE.md and ARCHITECTURE §10, and the two
phrasings were the same check.

**Happened:** they were not the same check, and both were wrong. Against one
attempt with `fired_at` set and no outbox row at all:

```
 claude_md_left_join | architecture_md_inner_join
---------------------+----------------------------
                   1 |                          0
```

**Why:** ARCHITECTURE used `JOIN`. An inner join only evaluates attempts that
*already have* a notice row, so it catches "notice queued but undelivered" —
the mild case — and silently passes "notice never queued at all", which is the
actual compliance disaster and the precise thing §6.6 exists to make
impossible. An invariant blind to the most severe instance of what it checks is
not partial coverage.

CLAUDE.md used `LEFT JOIN`, so it caught the severe case, but joined through
`o.payload->>'attempt_id' = a.attempt_id::text` when `outbox.attempt_id` is a
real foreign-key column sitting right there. Unindexable, and it breaks the
first time the payload shape changes.

**Changed:** one canonical form in both documents — left join on the real
column, `kind` in the `ON` clause so the outer join survives.

```sql
SELECT a.attempt_id FROM attempts a
  LEFT JOIN outbox o
    ON o.attempt_id = a.attempt_id AND o.kind = 'pre_debit_notice'
 WHERE a.fired_at IS NOT NULL AND o.delivered_at IS NULL;
```

Moving `kind` into the `WHERE` clause silently converts this back into an inner
join and restores the original bug. That is worth knowing before someone tidies
it.

Added `outbox_notice_lookup ON outbox (attempt_id, kind)` in the same pass. The
gate runs that lookup before every debit and `outbox_pending` cannot serve it —
that index is keyed on `deliver_by` and partial on undelivered rows, while the
gate asks about one specific attempt whose notice is normally already delivered,
which is exactly the set the partial index excludes. §5 had listed two indexes;
the gate being on the hot path was a gap in the design, not a deliberate
omission.

**The pattern, named.** Both of these were **checks that pass while proving
nothing**. That is a nastier class than a check that fails incorrectly: a
failing check gets investigated within minutes, a falsely-passing one gets
trusted for the rest of the project and quoted in the submission.

Two of the six invariants were in that state before a single line of application
code was written. The entire correctness claim here is "these six queries return
zero rows" — which means each one needs a test that makes it return *non*-zero
on purpose. **A green invariant nobody has ever seen go red is not evidence.**

**Test:** the reproduction above. Every invariant now gets a paired negative
case — seed the violation, assert the query catches it — before it is trusted to
return zero.

---

## Template

```
## YYYY-MM-DD · Short title

**Believed:**

**Happened:**

**Why:**

**Changed:**

**Test:**
```

---

## 2026-08-26 · The first host-side connection went to the wrong database

**Believed:** `DB_URL` in `.env.example` pointed at the compose container.
`docker compose ps` said healthy, `docker exec psql` worked, the schema was
verified. Nothing suggested otherwise.

**Happened:** the first test in `internal/store/` that opened a pool from the
host failed with `password authentication failed for user "quiescent"
(SQLSTATE 28P01)` — with the correct password, against a container whose
`POSTGRES_PASSWORD` is that exact string.

**Why.** A native Windows Postgres service was already listening on 5432.
Windows permits both it and Docker's port proxy to bind the same port, and
`localhost` resolved to the native one. Postgres deliberately reports a
nonexistent role as a password failure rather than leaking role existence, so
"wrong server" and "wrong password" produce the identical message.

Every prior schema verification had run *inside* the container via
`docker exec`, so nothing had ever crossed the host boundary. The collision was
invisible until the first process that had to.

This is the third instance of the pattern the earlier two entries named:
**a check that passes while proving nothing.** A green integration suite run
against a database that is not the system's database proves nothing at all, and
it would have failed in the most confusing possible direction — schema-shaped
errors on a server that simply does not have the schema.

**Changed:** a machine-local `.env` pins `POSTGRES_PORT=5433` and the matching
`DB_URL`. `docker-compose.yml` and `.env.example` keep 5432 as the default, so
the repository stays portable for a machine where the port is free — the
conflict is a property of this laptop, not of the project.

**Test:** `TestCreateCycleSeedsLeaseAtEpoch` reads back the row the `seed_lease`
trigger writes. It cannot pass against a server without the trigger, so it fails
if the pool ever reaches the wrong database again. The whole suite skips rather
than silently passing when `DB_URL` is unset.

---

## 2026-08-30 · `solve` computed a time-of-day that `due_date` cannot hold

**Believed:** a `schedule` test that scheduled a first attempt, then a retry
24h later, would see `retry.ScheduledFor == dueDate + 24h` — straightforward
arithmetic on a `time.Time` the test controlled from end to end.

**Happened:** the retry landed exactly 2 hours earlier than expected, every
time, with the offset itself (24h) correct. `TestScheduleRetryAttempt`
failed reproducibly; re-running it alone (not the slow, noisy first attempt
at diagnosing it) took well under a second, so the timing itself was never
the issue — the *value* was wrong.

**Why.** `mandate_cycles.due_date` is `DATE`, not `TIMESTAMPTZ` — correctly,
by design: a mandate is due *on a date*, not at an instant. The test built a
cycle with an arbitrary time-of-day (15:00-ish) baked into `DueDate` and
scheduled the first attempt against that in-memory struct — which worked,
because that call never round-tripped through Postgres. The *second*
`ScheduleNext` call used a cycle reloaded via `s.Cycle(...)`, and `DATE`
had silently truncated the time-of-day to midnight on the way in. `solve.Next`
then computed `midnight + 24h`, which is a real 2 hours earlier than
`(original 15:00-ish time) + 24h` — correct arithmetic on a value that had
already quietly lost precision.

This matters beyond the test: `schedule.ScheduleNext` **always** operates on
a cycle freshly loaded from the store — there is no code path where it sees
an in-memory `DueDate` with a real time-of-day. So in production,
`cycle.DueDate.Hour()` and `.Minute()` are **always zero**. `solve.First`
schedules the original attempt at exactly `dueDate` — which means, today,
the very first attempt of every cycle fires at precisely **00:00 UTC (05:30
IST)**, regardless of any intended business-hours policy. Nothing enforces
that this is a sensible firing time; it is simply whatever `DATE` happens to
decay to.

**Changed:** the test now seeds a `DueDate` that is already midnight UTC
(`realisticDueDate()`), matching what the column actually stores, so the
test asserts against real round-trip behavior instead of a value that was
never going to survive contact with the schema.

**Not changed, flagged instead:** the underlying gap — where the real
time-of-day for the *first* attempt should come from, since `due_date`
structurally cannot carry it — is unresolved. Added to `CLAUDE.md`'s open
items rather than guessed at under deadline pressure.

**Test:** `TestScheduleRetryAttempt`, `TestScheduleFirstAttempt` — both now
build their due dates the same way production data will actually arrive.

---

## 2026-08-30 · A second package made the first package's tests destructive

**Believed:** `internal/lease/` was small enough to build correctly the first
time — the same guarded-`UPDATE ... RETURNING` shape already proven in
`attempts` and `mandate_cycles`, closing **C4**. `TestC4StalledWorkerHandleNeverRefreshes`
failed on its first run with `guarded update matched no rows` — on the very
first acquisition of a freshly seeded, never-held lease, where the row's
`expires_at` defaults to 1970 and the guard cannot fail.

**Happened:** re-ran the same test alone, repeatedly — passed every time.
Ran `internal/store` and `internal/lease` together again — failed again, on a
different test this time. Not a logic bug; a test-isolation bug that only
exists once a second DB-backed package exists.

**Why.** `go test` runs separate package arguments as separate binaries, in
parallel, by default. `internal/store`'s `testStore()` helper opens with
`TRUNCATE audit_log, outbox, attempts, leases, mandate_cycles ... CASCADE` —
harmless when it was the only package touching the database, because every
row it creates uses a fresh random UUID and never collides with another
store test's rows. But `internal/lease`'s tests hit the *same* live Postgres
instance. When a `store` test's `TRUNCATE` lands between `internal/lease`'s
`CreateCycle` and its `AcquireLease` call, the cycle — and its trigger-seeded
lease row — simply no longer exists. The guarded `UPDATE` correctly reports
zero rows matched, because zero rows exist. The mechanism was never wrong;
the ground it stood on was cut away mid-test by an unrelated package.

This is the same family as the two earlier host-boundary and invariant bugs
in this journal: **a check that fails for a reason that has nothing to do
with what it's checking.** `TestCyclesByStateAndNotFound` genuinely needs
that `TRUNCATE` — it asserts `len(cycles) == 1` against the whole table — so
removing it breaks a real invariant instead of fixing the race.

**Changed:** DB-backed packages are no longer safe to `go test` together with
default parallelism. Documented here rather than papered over with a helper
change: run `go test -p 1 ./internal/store/... ./internal/lease/...` (and any
future DB-backed package) whenever more than one is under test together.
`harness/` (cycle 7) will need the same discipline once it exists.

**Test:** `TestC4StalledWorkerHandleNeverRefreshes` and
`TestC4FenceIsMonotonicAcrossExpiry` — both pass deterministically under
`-p 1`, and both were widened from a 10ms/50ms timing margin to 1ms/300ms
after the first version flaked once on a just-started, cold Docker container.

---

## 2026-08-30 · The notice gate checked "delivered," not "delivered in time"

**Believed:** `store.NoticeDelivered` — the fire-time gate from mechanism #6
— correctly enforced constraint #3, the 24-hour pre-debit notice. It was
tested (`TestD2NoticeGateOpensOnlyAfterDelivery`), the test was green, and
the gate had been trusted since `store/` was first built.

**Happened:** nothing crashed — this was caught by re-reading the actual
query, not by a failing test. `NoticeDelivered` only checked
`delivered_at IS NOT NULL`. A notice delivered one minute before firing
would open the gate exactly the same as one delivered two days before. The
"24 hours" in constraint #3 was never actually compared against anything —
it existed in `docs/SOURCES.md` (Source: RBI e-mandate framework,
**Confidence: High** — not a guess, not the disputed NPCI question) and in
prose, but not in the query that was supposed to enforce it.

**Why.** The gate was built to answer "did a notice get sent," which is a
real question and a necessary one — but not the question constraint #3
actually asks, which is "was the customer given a real 24-hour window."
`TestD2NoticeGateOpensOnlyAfterDelivery` passed because it only ever
delivered the notice and then immediately checked the gate — it never
constructed a case where delivery happened, just too close to firing. A
green test had been asking a narrower question than the invariant it was
named for.

**Changed:** `NoticeDelivered` now takes the attempt's `scheduledFor` and
checks `delivered_at <= scheduledFor - 24h`, not just `IS NOT NULL`. The
24-hour figure itself was pulled out of `solve/`'s private constant into
`domain.NoticeLead`, so both packages read the same number instead of two
copies of "24 hours" quietly drifting apart. Invariant 6 in `CLAUDE.md`
updated to match — it had the identical gap.

**Not yet changed:** catching this *only* at fire time means a doomed
attempt sits reserved for up to 24h+ before anyone finds out its notice is
going to be too late. The better fix — cancel and refund the moment
delivery happens (or its deadline passes) too late to still work, inside
the notice-relay loop — belongs to `outbox/` (cycle 6), not yet built.

**Test:** `TestNoticeGateStaysClosedWhenDeliveredLessThan24hAhead` — delivers
a notice at exactly 23 hours ahead and asserts the gate stays shut.
`TestD2NoticeGateOpensOnlyAfterDelivery` updated to deliver at a real 25
hours ahead, so it no longer accidentally proves the narrower, wrong claim.

---

## 2026-09-04 · The state-machine entry above was never actually true

**Believed:** the entry dated 2026-08-25, *"The state machine on paper is not
the state machine in code,"* documented a real fix — a scheduler claim query
reading `WHERE state NOT IN ('unknown', 'held')`, verified by
`TestSchedulerSkipsUnknownAndHeld`.

**Happened:** grepping the repository for both turned up nothing. Neither the
query nor the test exists anywhere, in any package. `schedule/` — the package
that entry describes fixing — was not built until later; its real tests are
dated 2026-08-30 and cover none of this. The entry was written ahead of the
code, describing an intended fix, and the follow-through never happened.

**Why.** The diagnosis in that entry is correct — it names the right failure
and the right shape of fix. What's wrong is that it was written in the past
tense, as something verified, when it was only planned. Nothing ever checked
the journal against the code it claims to describe. This is the same family
as the invariant 5 and invariant 6 entries above — a claim that reads as
settled while proving nothing — except the false-green here is in the
documentation instead of a query.

**Changed:** nothing about the state machine itself in this entry — that's
the next one. This entry exists so the record stays honest: the 2026-08-25
entry described an intention, not a result, and stayed that way for over a
week, unnoticed, including by the person who wrote it.

**Test:** none — this is the correction, not the fix. See the next entry.

---

## 2026-09-04 · D6, for real this time — the scheduler had no way to see a cycle mid-flight

**Believed:** wiring `cmd/quiescent/main.go`'s scheduler loop would be
mechanical — `domain.State` already defines `scheduled`, `in_flight`, and
`unknown`, so surely something already sets them.

**Happened:** nothing did. Only `recovered`, `held`, and the refund-path
`pending` were ever written by any store method. A scheduler polling
`CyclesByState(pending)` would see a cycle as claimable at every tick from
the moment it was created until it reached a terminal state — including the
entire multi-day gap between reserving an attempt and it actually firing,
and including the window where a fired attempt is stuck waiting on a
`TIMEOUT` to be reconciled. That second case is D6 itself: the scheduler
re-claiming a cycle that is still mid-flight, risking a second debit on top
of one whose outcome isn't known yet.

**Why.** The domain vocabulary — all eight states — was designed up front in
cycle 0, before any of the mechanisms meant to maintain it existed. Each
later package (`schedule/`, `execute/`, `reconcile/`) was built against only
the part of the state machine it personally needed, and nothing ever came
back to close the loop on the rest.

**Changed:** every transition now has a real write site, in the same
transaction as the event that causes it:

- `ReserveAttempt` sets `state = 'scheduled'` alongside the budget CAS.
- `MarkAttemptFired` sets `state = 'in_flight'`.
- `RecordAttemptOutcome` sets `state = 'unknown'` when the outcome is
  `TIMEOUT` — and only then. `SUCCESS`/`FAILURE` need `execute` to weigh in
  with `classify` and the remaining budget before deciding
  `recovered` / `pending` / `escalated` / `abandoned`, so `store` records
  what happened and leaves that decision to the caller.
- `AbandonAttempt`'s refund path now also returns the cycle to `pending`,
  matching what `ResolveNotDebited` already did.
- A new `store.SchedulableCycles` query replaces `CyclesByState(pending)` as
  the scheduler's actual claim query. It filters on `state = 'pending'`
  **and** `NOT EXISTS` an attempt with `outcome IS NULL` or
  `outcome = 'TIMEOUT'`. The second clause is deliberate belt-and-suspenders:
  if a future bug ever leaves `state` wrong, the query still can't be
  fooled, because it checks the attempts table directly instead of trusting
  the label.
- A `state_valid` CHECK constraint (migration `000002`) makes any other
  value in that column impossible at the database level, not just
  discouraged in application code.

**Test:** `TestReserveAttemptSetsCycleScheduled`,
`TestMarkAttemptFiredSetsCycleInFlight`, `TestRecordTimeoutSetsCycleUnknown`,
`TestD6SchedulableCyclesExcludesCycleStuckInUnknown`, and the one that
actually earns the D6 name —
`TestD6SchedulableCyclesCatchesUnresolvedAttemptEvenIfStateWasWronglyPending`,
which seeds a cycle exactly the way this project would have shipped it
without this fix (state stuck at `pending`, one unresolved attempt),
confirms the naive `CyclesByState(pending)` query really does return it, and
then confirms `SchedulableCycles` excludes it anyway.

---

## 2026-09-04 · C4, witnessed properly — the stalled worker's fence never moves, even when asked to

**Believed:** the fencing mechanism (mechanism #2) was built correctly the
first time, and `TestC4StalledWorkerHandleNeverRefreshes` /
`TestC4FenceIsMonotonicAcrossExpiry` already prove it. True — but no entry in
this journal ever showed *why* it matters, or what the naive alternative
actually does. CLAUDE.md calls C4 "the most important item in the project";
it deserved more than an untold test.

**Happened (verified today, not at original build time):**
`TestC4StalledWorkerHandleNeverRefreshes` confirms worker A's captured fence
(1) never changes after it stalls past its lease TTL, and worker B's later
acquisition correctly advances to fence 2. Then, to see what the *naive*
mistake — re-reading the fence instead of using the one captured at
acquisition — would actually do: a new test,
`TestC4RawFenceReadWouldCollideIfEverExposed`, reads the `leases` row
directly, bypassing the guarded acquisition path entirely. It returns **2**
— worker B's fence, not worker A's own 1. If worker A's code used that raw
value instead of the one it captured, its debit would carry the identical
fence B is using. The provider (mechanism #3, `<` not `<=`) accepts a
request whose fence *equals* the highest seen — deliberately, so a
legitimate retry after a network blip isn't rejected. That same tolerance is
exactly what a stalled worker presenting a re-read fence would exploit.

**Why.** The fence is proof of *when* you acquired the lease, not a live
property of the cycle you can check again later. A worker that re-reads it
is asking "who holds this right now," which is a different, wrong question
— by the time it asks, the answer has already changed.

**Changed:** nothing — the mechanism was already correct. What changed is
that this is now demonstrated, not just asserted. `execute.Worker.FireOne`
captures `handle.Fence` once, from `lease.Acquire`'s return value, and never
re-reads it. No exported function in `store` exposes a raw fence read; the
only way to learn a fence is through `AcquireLease`'s guarded `RETURNING`,
which requires actually winning the lease. That's not a coding-style choice
— it's the reason the naive mistake above cannot be written by accident.

**Test:** `TestC4StalledWorkerHandleNeverRefreshes`,
`TestC4FenceIsMonotonicAcrossExpiry` (the shipped mechanism, both re-run
today), `TestC4RawFenceReadWouldCollideIfEverExposed` (new — demonstrates
what the naive alternative would have handed a stalled worker),
`TestFenceRejectsStrictlyLowerAndAcceptsEqual` and
`TestConcurrentStaleFenceNeverAdmitted` (the receiver-side rejection,
`internal/provider`).

---

## 2026-09-04 · C3 — the log records what was known, not what happened

**Believed:** write-ahead (mechanism #4) needed no new demonstration — the
code is three lines: insert the attempt row before sending the debit.
Simple enough that "watching it break" felt like theatre.

**Happened:** ran `TestC3FireOneMarksTimeoutWhenBankNeverReplies` for real
today, with an injected `timeoutAfterCommit` fault against a live
provider-sim. The bank actually decided the debit —
`outcome=FAILURE failureCode=TECHNICAL_DECLINE`, committed to its ledger —
but the connection was severed before the worker's HTTP call returned. The
worker recorded `TIMEOUT`, not a guess at `FAILURE`. Log line, unedited:
*"worker recorded TIMEOUT; bank actually decided FAILURE — exactly the gap
reconciliation exists to close."*

**Why this is C3, not theatre.** The naive version most systems ship writes
the attempt row *after* a successful response, since that's when you "know"
what happened. C3 is what that costs — a crash (or, as reproduced here, a
severed connection) between sending and recording leaves *nothing* in the
database. On restart, the naive version sees no record of the attempt at
all, concludes it never happened, and fires again.
`TestC3WriteAheadRejectsPreResolvedAttempt` (store-level) proves the other
half: the write-ahead row is inserted with `outcome IS NULL`, and the schema
itself refuses an attempt inserted any other way.

**Changed:** nothing today — write-ahead was built correctly the first
time, per CLAUDE.md's own judgement call for mechanisms whose failure mode
is obvious. What's new is seeing it, deliberately, against a real severed
connection instead of trusting the three-line description.

**Test:** `TestC3FireOneMarksTimeoutWhenBankNeverReplies` (`internal/execute`),
`TestC3WriteAheadRejectsPreResolvedAttempt` (`internal/store`) — both re-run
today against live Postgres and a live provider-sim instance, not mocks.

---

## 2026-09-04 · C5 — two schedulers, one cycle, only one wins

**Believed:** the version-guarded CAS in `ReserveAttempt` (mechanism #1) was
already proven by `TestC5ReserveAttemptRejectsStaleVersion` — a single
caller presenting a version that's already moved.

**Happened:** ran `TestC5ScheduleConcurrentRaceOnlyOneWins` today, the
sharper version of the same claim — two goroutines calling
`schedule.ScheduleNext` on the identical, freshly-loaded cycle at the same
time, not a caller manually supplying a stale version after the fact. Real
log output: one goroutine gets `attempt scheduled ... seq=1`; the other gets
`budget reservation raced; cycle changed underneath us expectedVersion=0`
and returns `ResultBudgetRaced` rather than a second attempt.

**Why.** `mandate_cycles.attempts_used` is read once by each caller before
either writes. Without the version check, both would read the same starting
count, both would conclude budget remains, and both would insert an attempt
— `attempts_used` becomes 5 against a 4-attempt regulatory cap, and neither
caller did anything individually wrong; the bug is in treating
read-then-write as atomic when it isn't.

**Changed:** nothing new — `ReserveAttempt`'s
`WHERE cycle_id = $1 AND version = $2 AND attempts_used < $3` was mechanism
#1 from the start. What's new is proof at the layer that actually matters
for this failure: not just the SQL statement in isolation (already
covered), but two real concurrent callers of the real `schedule.Scheduler`
racing on the real store.

**Test:** `TestC5ReserveAttemptRejectsStaleVersion` (`internal/store` —
single stale caller), `TestC5ScheduleConcurrentRaceOnlyOneWins`
(`internal/schedule` — genuine concurrent goroutines), both re-run today.
`budget_cap CHECK (attempts_used <= 4)` is the schema-level backstop if both
somehow got past the CAS.

---

## 2026-09-04 · A test failed because of what time it was run

**Believed:** `TestFireOneRecordsDeterministicOverLimitFailure` was a purely
deterministic test — an amount over the UPI cap, a hash-based draw seeded to
42, nothing that should care what time it is.

**Happened:** running the full suite live, at 12:42 IST, it failed:
`failureCode: got 0xc000114450 want AMOUNT_EXCEEDS_LIMIT` — the debit came
back `TECHNICAL_DECLINE` instead. 12:42 IST sits inside the 10:00–13:00
blocked window.

**Why.** `provider.Decide` checks `Blocked(c.FiredAt)` before it checks the
amount cap. That ordering is correct — a blocked-window decline should win
over every other reason, since it's the one the regulator cares about most.
But `firedAt` inside `provider.Sim` was always `time.Now()`, and nothing in
`internal/execute`'s test helper (`testBank`) ever pinned it, unlike
`internal/provider`'s own tests, which already had this exact pattern
(`s.Now = func() time.Time { return at(t, ...) }`) for precisely this
reason. The test was deterministic in the seed and never deterministic in
the clock — a test that only fails during two ~3.5-hour windows a day is a
green test for the wrong reason the other ~20.5 hours, which is the same
family of bug as the invariant 5 and invariant 6 entries above.

**Changed:** `provider.Sim.now` exported as `Sim.Now`, matching the pattern
`execute.Worker.Now` and `outbox.Relay.Now` already use. `internal/execute`'s
`testBank` now pins `sim.Now` to a fixed instant outside both blocked
windows, so the entire `execute` test suite is time-of-day-independent —
not just the one test that happened to catch this.

**Test:** `TestFireOneRecordsDeterministicOverLimitFailure` re-run
immediately after the fix, still at 12:4x IST, inside the same blocked
window that broke it minutes earlier — passes now regardless of wall-clock
time.

---

## 2026-09-04 · A too-late notice got re-abandoned on every single tick, forever

**Believed:** `outbox.ProcessOne` fully handles a notice that missed its 24h
deadline — abandon the attempt, refund the budget, done.

**Happened:** noticed while running `cmd/quiescent`'s worker role live
against a real thundering-herd test: the same `attemptID` logged *"notice
missed its 24h deadline"* on every relay tick, indefinitely, for an attempt
that had already been abandoned minutes earlier.

**Why.** `PendingNotices` selects `WHERE delivered_at IS NULL`. A too-late
notice never gets `delivered_at` set — correctly, since it genuinely wasn't
delivered — so it never leaves that query. `ProcessOne` re-ran on it every
tick, calling `AbandonAttempt` again each time; that call correctly no-ops
(the attempt's already resolved, so the guarded `UPDATE` matches zero rows
and returns `ErrConflict`, which `ProcessOne` already tolerates), so nothing
was ever double-refunded — but the warning log fired forever, and every
worker process wasted a query on a notice that was already fully handled.

**Changed:** `PendingNotices` now joins `attempts` and adds
`AND a.outcome IS NULL` — a notice only counts as pending if its attempt
hasn't been resolved one way or another yet. No new column on `outbox`, no
second place for "is this handled" to live — reuses the attempt's own
outcome as the single source of truth, the same pattern `SchedulableCycles`
already uses for D6.

**Test:** `TestPendingNoticesExcludesAlreadyAbandonedEntries` — abandons an
attempt via a too-late `ProcessOne`, then asserts a follow-up
`PendingNotices` call no longer returns it.

---

## 2026-09-04 · Wiring `predict.PreferredHour` into the harness proved it currently does nothing

**Believed:** the harness never exercised `predict.PreferredHour` simply
because nobody had gotten around to passing it through — a wiring gap, not
a design question.

**Happened:** wiring it in required more than passing a different argument.
`GenerateCycles` created one cycle per synthetic customer — no customer
ever had a second cycle, so `PreferredHour`'s 2-success minimum could never
be met; there was never any history to learn from. Added
`GenerateCustomerSequences` (6 monthly cycles per customer) and
`SimulateCustomerSequence`, which threads a customer's real successful-fire
times forward as history between their cycles, then reran the full 200-seed
measurement `CLAUDE.md`'s Measurement section calls for:

```
seeds=200 customersPerSeed=50 cyclesPerRun=300
system=0.6160 baseline=0.6162 oracle=0.6399
achievableLift=0.0237 capturedLift=-0.0002 capturedPct=-0.99% (+/- 0.0001, 95% CI)
```

The system and baseline are statistically indistinguishable. Captured lift
is ~0, not because the wiring is broken, but because there is currently
nothing for `PreferredHour` to capture.

**Why.** `solve.Next` fixes *which day* an attempt fires on (T+24h/72h/7d —
the conservative reading of the still-unresolved NPCI spacing question,
constraint box item 3) — identical to the baseline's days, by construction.
The only two levers `solve`/`predict` have beyond the baseline are avoiding
blocked windows and now, preferring a customer's historically successful
*hour*. But `provider.World.BalanceAt` — and therefore
`SuccessProbability` — varies by **day of month**, never by hour of day, in
the simulated world. Shifting which hour you fire at, as long as it's
outside a blocked window, changes nothing about the odds. `PreferredHour`
is correctly wired; the world model simply gives it no lever to pull while
the day stays fixed.

The oracle result is the useful part of this: it proves **2.37 percentage
points of lift genuinely exist** (63.99% vs. 61.62%) — the system just
can't reach any of it under the current conservative day-fixing rule. That
number is the concrete stake attached to constraint box item 3 (NPCI
spacing mandated-or-not, still unverified) — resolving it in favor of free
day selection is the only thing that could unlock this lift, not further
tuning of `predict`.

**Changed:** `harness.Outcome` gained a `FiredAt` field so a customer's real
fire time (not a guess) feeds their next cycle's history.
`harness.RunBatch`/`RunMany` now operate on customer sequences instead of
independent one-off cycles. Nothing about `solve`/`predict` changed — this
entry is a measurement result, not a fix.

**Not changed, flagged instead:** whether to reopen constraint box item 3
given this number. Deliberately left as a decision for the person deciding
what ships, under deadline pressure, rather than guessed at here.

**Test:** `TestReport200Seeds` (`internal/harness`) — reproduces the numbers
above deterministically; `TestSimulationsAreDeterministicUnderTheSameSeed`
and `TestOracleNeverRecoversFewerCyclesThanSystem` still pass unchanged.

---

## 2026-09-04 · The audit trail existed, but nothing in production ever wrote to it

**Believed:** `store.AppendAudit` and the `audit_log` table meant the
append-only decision log was done — it had a test
(`TestAppendAuditRoundTrips`) proving the write worked.

**Happened:** grepping for callers of `AppendAudit` outside test files
turned up none. `schedule.ScheduleNext`, `execute.FireOne`, and
`reconcile.Resolve` — the three places this system actually makes a
decision — never called it. The mechanism was real; nothing in the real
system ever exercised it.

**Why.** Building the write path and proving it round-trips is a different
task from calling it from the code that makes decisions, and nothing forced
the second half to happen — no test failed, no invariant caught it, because
"is anything calling this" isn't something a unit test for the function
itself can ever show.

**Changed:** all three call the same pattern now — after the real decision
already committed, append one audit row with `CorrelationID` set to the
attempt's own `AttemptID` (no schema change; every entry about one attempt
threads together for free), and log-but-don't-fail on an audit write error,
since the real decision already succeeded and shouldn't be undone by a
logging failure. Added `store.AuditByCycle` — reading the trail back had no
path either. `CLAUDE.md`'s package layout corrected: there is no separate
`audit/` package; a wrapper around two `store` functions would have added
nothing.

**Test:** `TestScheduleNextAppendsAnAuditEntry`, one new assertion inside
`TestFireOneRecordsSuccessOrFailureFromBank`, and one inside
`TestResolveReturnsToPendingWhenNotDebited` — each confirms a real audit row
lands after the real operation, against live Postgres.

---

## 2026-09-05 · Reconciliation only caught the crash it caused itself, not a real one

**Believed:** `store.NeedsReconciliation` finds every attempt that needs
reconciling — that's what its name says, and `TestC3FireOneMarksTimeoutWhenBankNeverReplies`
(2026-09-04) already proved the write-ahead half of C3 works.

**Happened:** found live, by the person using the CLI, not by a test. A
`worker` process left a real attempt stuck: `fired_at` set, `outcome`
completely empty — and it stayed stuck forever, `AbandonAttempt` failing on
every retry with `guarded update matched no rows`. Confirmed against the
actual row: `fired_at = 2026-03-08 05:30:00+00`, `outcome` NULL, cycle
`state = in_flight`.

**Why.** `NeedsReconciliation`'s query was `WHERE outcome = 'TIMEOUT'`.
That value only ever gets written by one specific code path in
`execute.FireOne` — the one where `w.Bank.Debit` returns a Go error *within
the same process*, and the running goroutine is still alive to catch it and
call `RecordAttemptOutcome(..., OutcomeTimeout, ...)`. A genuine crash —
the whole process dying between `MarkAttemptFired` and the debit call
returning, which is the literal definition of C3, "crash after sending,
before recording" — never runs that line at all. The row is left with
`outcome` still NULL, not `'TIMEOUT'`, and a query that only looks for the
string `'TIMEOUT'` never finds it. `TestC3FireOneMarksTimeoutWhenBankNeverReplies`
passed because it only ever tested the in-process-error shape (an injected
`timeoutAfterCommit` fault the same process observes and handles) — it
never tested an actual crash, so it never could have caught this. Same
family as the invariant 5/6 and D6-entry false-greens above: a test that
answers a narrower question than the one its name claims to.

`AbandonAttempt` was correctly refusing to touch this row the whole time —
it's guarded on `fired_at IS NULL`, precisely so a fired-and-unknown attempt
can never be waved away as if it never happened. The system wasn't wrong to
refuse; nothing was ever going to pick the row up to resolve it properly.

**Changed:** `NeedsReconciliation` now also matches
`fired_at IS NOT NULL AND outcome IS NULL`, not just `outcome = 'TIMEOUT'`.
`ResolveDebited` and `ResolveNotDebited` widened to match — they used to
require `outcome = 'TIMEOUT'` exactly; now `fired_at IS NOT NULL AND
(outcome = 'TIMEOUT' OR outcome IS NULL)`. No new machinery: reconciliation
already acquires the lease first (2026-08-25 entry), so a truly in-flight
attempt (lease still held) is untouched regardless of which rows the query
returns — widening the query is safe because that guard was already doing
its job.

**Test:** `TestC3ReconciliationCatchesAGenuineProcessCrashNotJustAnExplicitTimeout`
— a new `fireAndCrash` helper (mark fired, call the bank, stop — no
`RecordAttemptOutcome` at all, unlike the existing `fireAndTimeout`), then
asserts `NeedsReconciliation` finds it and `Resolve` actually resolves it.

---

## 2026-09-05 · The scheduler spammed the same duplicate-key error forever

**Believed:** `attempts_used` is "how many attempts this cycle has spent" —
a single number, safe to use for both the regulatory budget check and for
picking the next attempt's sequence number.

**Happened:** found live, by the person using the CLI. An attempt got
abandoned (its pre-debit notice missed the 24h deadline) and its budget
refunded — correct. The scheduler then tried to schedule a fresh attempt
for the same cycle and hit `store: insert attempt: store: duplicate key:
attempts_idempotency_key_key` — and kept hitting it, identically, every
~5 seconds, forever, in the live scheduler log.

**Why.** `attempts_used` is refundable — abandon-and-refund correctly
brings it back down. But `attempts` is append-only (`docs/JOURNAL.md`'s own
working agreement: never delete, mark instead), so the abandoned row and
the idempotency key it holds (`cycleID:seq`) never go away. `seq` was
computed as `attempts_used + 1` — after a refund, that arithmetic
recomputes a seq number that's already permanently claimed by the
abandoned row. The `INSERT` was doomed before it ran, and nothing about
the retry loop would ever change that, so it repeated identically forever.
Not a crash, not a race — a plain conflation of two different numbers
that happened to start out equal.

**Changed:** `store.ReserveAttempt` now computes `seq` from
`SELECT count(*) FROM attempts WHERE cycle_id = $1` inside the same
transaction as the budget reservation — a count that only ever grows,
independent of refunds. `schedule.go` and `cmd/quiescent/create.go` no
longer guess `seq`/`IdempotencyKey` themselves; `ReserveAttempt` now
returns the finalized attempt. Separately, `execute.FireOne`'s two
`AbandonAttempt` calls now tolerate losing a race to the outbox relay
(`store.ErrConflict`) the same way `outbox.ProcessOne` already did,
instead of logging it as a hard error.

**Test:** `TestC10SeqSurvivesAnAbandonAndRefund` — reserve seq=1, abandon
and refund it, reserve again on the same cycle, assert the second attempt
gets seq=2 and a distinct idempotency key. `TestFireOneToleratesLosingTheAbandonRaceToNoticeRelay`
— pre-abandon an attempt out of band, then call `FireOne` on it, assert it
returns cleanly instead of propagating the conflict as an error.

---

## 2026-09-05 · A correctly-refused retry could never schedule itself out of the refusal

**Believed:** the notice-lead check (`docs/FAILURES.md` D2) and the fixed
T+24h/72h/7d retry slots (`solve.Next`) were two independent, already-proven
mechanisms. Fixing C10 above and re-testing live was expected to show a
clean retry, nothing more.

**Happened:** it didn't. The retry that C10 unblocked immediately hit a
second, different problem: scheduled, then abandoned again — safely, no
error — and rescheduled identically, forever, never firing. `verify`
stayed green throughout; nothing was ever technically violated. The cycle
just never moved.

**Why.** Retry slots are computed from the *due date*
(`dueDate.Add(offsets[slotIndex])`) — a fixed calendar anchor. The
notice-lead requirement is computed from *now* — the moment the retry
decision is actually made, which is whenever the previous attempt failed.
These two clocks agree by coincidence, not by design. If a failure happens
late enough in its own day, `dueDate + 24h` lands less than 24 hours after
the failure itself — the notice deadline for that slot has already passed
before the slot is even chosen. The worker correctly refuses to fire
without a real notice and refunds the attempt; the scheduler then picks
the exact same doomed slot again, because nothing about the inputs changed.
Confirmed with the user before touching this: a fresh 24h notice really is
required before *every* attempt, retries included — so the fix had to
change the scheduling, not the notice rule.

**Changed:** `solve.First` and `solve.Next` now take `now` as an explicit
argument (still pure — no internal clock read) and never return a
`scheduledFor` closer than `domain.NoticeLead` to it; when the mandated
slot doesn't leave enough room, the fire time is pushed forward — never
earlier — to `now + NoticeLead`, then re-checked against the blocked
windows in case the push landed inside one. The shift, when it fires, is
recorded in a new `ReasonConstraints.NoticeLeadShift` field, so the audit
trail explains it rather than leaving a silent adjustment. `schedule.go`
gained a `Scheduler.Now func() time.Time` (mirroring `execute.Worker`'s
existing pattern) instead of calling `time.Now()` itself. `harness.SimulateSystem`
threads a `decidedAt` through the same way — 30 days before the due date
for the first attempt, the previous attempt's own `scheduledFor` for every
retry after that — so the measurement harness now reflects the same real
constraint the live system does, rather than silently assuming every
mandated slot always has room to spare.

**Test:** `TestC11NextNeverSchedulesSoonerThanTheNoticeCanBeDelivered`,
`TestC11FirstNeverSchedulesSoonerThanTheNoticeCanBeDelivered`, and
`TestC11NoticeLeadClampNeverLandsInsideTheBlockedWindow` (sweeps all 24
hours of failure time, asserts the clamped result is always both ≥24h out
and outside both blocked windows). Verified live afterward: the same
cycle that used to spam forever now schedules its retry for a real time
with room for a real notice, and the notice actually sends.
