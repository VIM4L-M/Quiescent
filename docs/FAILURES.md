# Failure taxonomy

Twenty-nine ways this system can fail, grouped into five categories.

**Failure IDs are load-bearing.** They appear in prompts, in `JOURNAL.md`
entries, and in test names — `TestC4StalledWorkerRejectedByFence`. When a
countermeasure is implemented, it names the ID it closes.

| | Category | Count | What it means |
|---|---|---|---|
| **A** | Known Declines | 6 | The bank refused and told us why |
| **B** | Ambiguous Outcomes | 3 | We sent it and got silence |
| **C** | Execution Failures | 9 | Our own process crashed, froze, or raced |
| **D** | Policy Violations | 6 | Nothing broke — we did something we shouldn't have |
| **E** | Recovery Harm | 5 | Coming back caused more damage than the outage |

**A** is the input. **B** is why `unknown` exists. **C** is the differentiator.
**D** is what the invariant queries catch. **E** is why stopping matters.

**The five worth witnessing before fixing:** C3, C4, C5, C7, D6. Everything
else is built correctly the first time.

---

## A — Known Declines

The transaction failed for a normal, expected reason and the bank gave a clear
code. These are not bugs — this is the *input* to the system. What matters is
that different codes need completely different responses.

### A1 — Insufficient funds

**Why** The mandate fires on the due date; the balance isn't there at that
moment. Nothing is broken — the timing doesn't match when money is in the
account.

**Example** Rahul's gym mandate fires on the 5th for ₹2,000. Balance ₹340.
Salary lands on the 7th.

**Affects** One attempt spent. But this is the *most recoverable* failure in
the system — he wants to pay and will have money in two days. Handled well you
get the money; handled badly you burn three more attempts and lose ₹2,000 that
was always going to arrive.

**Countermeasure** Classify `retry_later`. Look at his history — four of the
last five cycles succeeded on the 8th. Schedule for the 8th, not a fixed 24
hours later.

### A2 — Bank unavailable / technical decline

**Why** The bank's systems are down or overloaded. Nothing to do with the
customer. Can also be an NPCI peak-hour block — fired at 10:30, refused at the
door.

**Example** HDFC's UPI is down at 2am. Rahul has ₹12,000. The debit fails
anyway.

**Affects** An attempt spent for a reason unrelated to whether the money
exists. The most *wasteful* failure — the customer was ready and the
infrastructure got in the way.

**Countermeasure** Classify `retry_soon`. Hours, not days. Retry **off-peak**,
or you burn another attempt on the same wall.

### A3 — Mandate revoked

**Why** The customer cancelled the standing permission. Quit the gym, or was
annoyed at three failed debits.

**Example** Rahul cancels on the 10th. The mandate is dead. Every future debit
fails.

**Affects** The trap. It *looks* retryable and is mathematically impossible —
attempt 2 fails identically to attempt 1, and 3, and 4. A system that treats
all failures as retryable spends the whole cycle window knocking on a locked
door while the one action that would have worked goes undone.

**Countermeasure** Classify `hard_decline`. **Do not retry — not even once.**
Escalate: ask for a new mandate. This converts a retry problem into a
re-authorization problem.

### A4 — Mandate expired or paused

**Why** Every mandate has an end date. Month 13 arrives and the permission has
quietly lapsed. Or it was paused.

**Example** Signed March 2025 for 12 months. On 5 April 2026 the gym fires. It
expired a month ago.

**Affects** Same shape as A3, slightly worse — it's often *our* oversight. The
expiry date was known in advance.

**Countermeasure** `hard_decline` → escalate for renewal. Paused mandates can
wait for resume instead.

### A5 — Amount exceeds mandate limit

**Why** The mandate has a ceiling. The merchant raises the fee above it. The
bank refuses — the customer never agreed to that amount.

**Example** Gym fee goes ₹2,000 → ₹2,500. Rahul has ₹15,000. Still fails.

**Affects** The cruellest one. He has the money and would pay happily. The
*cap* is wrong, so retrying ₹2,500 fails forever regardless of balance.

**Countermeasure** `hard_decline` → escalate for mandate amendment. **The
clearest proof of why classification matters** — balance fine, customer
willing, money exists, and retrying still gets nothing.

### A6 — Account closed / frozen / invalid

**Why** The account was closed, frozen by legal order, or the number was wrong
from the start.

**Example** Rahul switches banks and closes his HDFC account. The mandate still
points at it.

**Affects** Nothing recoverable. Unlike A3–A5 there's no re-auth that fixes it —
there is no account. Frozen may have legal implications; invalid points to a
data-quality bug at registration.

**Countermeasure** Classify `terminal`. Stop immediately, mark `abandoned`,
flag. Don't escalate to the customer either — there's nothing to ask for.

### The pattern across A

| Class | Codes | Action |
|---|---|---|
| `retry_later` | Insufficient funds | Wait for money, then retry |
| `retry_soon` | Bank down, technical decline | Wait hours, retry off-peak |
| `hard_decline` | Revoked, expired, over limit | **Never retry.** Escalate for re-auth |
| `terminal` | Closed, frozen, invalid | Stop. Nothing to do. |

> Retrying a hard decline doesn't merely waste an attempt — it spends the
> cycle window on an action that cannot work, while the action that would have
> worked goes undone.

---

## B — Ambiguous Outcomes

In A the bank answered. Here it didn't. The question isn't "did it fail," it's
**"did anything happen at all?"**

### B1 — Timeout after sending

**Why** The connection dies before a reply arrives. Network drop, server hang,
load balancer timeout. Two possibilities, identical from where we stand: the
request never arrived, or it arrived, money moved, and the *reply* was lost.

**Example** 18:00:00 debit sent. 18:00:03 connection drops. Same silence either
way.

**Affects** Every obvious option is wrong. Retry → possibly ₹4,000 charged.
Mark failed → books say unpaid when paid. Mark success → merchant short ₹2,000.
Guess → wrong half the time, with money. **There is no correct answer available
at that moment.**

**Countermeasure** Move to `unknown` — not an error, an accurate description of
reality. Do **not** retry. Ask a *different question* instead: "what happened to
request `abc123`?" A read, not a write. Safe to repeat, cannot cause a debit,
does not consume attempt budget.

### B2 — Timeout where the money actually moved

**Why** Same as B1 with the worst timing — the bank *committed* the debit and
then the connection dropped before confirmation.

**Example** Rahul's account ₹2,000 → ₹0. The gym's money is in flight. Our logs
say "timeout." Our database says `in_flight`.

**Affects** This is what makes blind retry catastrophic. Retry and he's charged
₹4,000 he doesn't have. Overdraft, penalty fees — all because the system
couldn't tell "I don't know" from "it failed."

**Countermeasure** Identical to B1, and that is the point — **B2 cannot be
distinguished from B1 at the moment it happens.** Reconciliation separates
them, afterwards. The idempotency key is what makes the query answerable:
without it you'd be asking "did you charge Rahul ₹2,000 recently," which is
unanswerable if he has three legitimate charges.

### B3 — Reconciliation can't resolve it

**Why** We asked; the bank doesn't know either. Settlement pending, their
system mid-recovery. Genuinely unresolved on their side too.

**Example** Query `abc123` five times over an hour. Every time: "processing."

**Affects** Every automated path exhausted. No query left to run. The
information does not exist anywhere reachable.

| | Who doesn't know | Fixable by asking? |
|---|---|---|
| **B1** | Us | Yes — ask the bank |
| **B3** | Us *and* the bank | No — nobody has it |

**Countermeasure** Move to `held`. Stop. Flag for a human — who can call the
bank, read a settlement file, check with the merchant. Not the system giving
up; **ownership transferring** to someone with access we don't have.

> Exactly-once delivery across a network does not exist. At-least-once with an
> idempotent receiver, or at-most-once with reconciliation. No third option.
> This system chooses reconciliation for money movement.

---

## C — Execution Failures

Nothing wrong with the bank or the customer. **Our own code is the problem.**
The biggest category, and the differentiator — no competing implementation
handles any of it.

Firing a debit is four steps, and a crash can land in any of the three gaps:

```
1. write attempt row → 2. send debit → 3. bank processes → 4. record outcome
```

### C1 — Crash before the attempt row is written

**Why** The worker takes the lease, then dies before writing anything.

**Example** 18:00:00 lease acquired. 18:00:01 container killed.

**Affects** Almost nothing. No row, no debit, nothing happened. The lease
expires in 30s and another worker takes over. The burned fence number is free —
fences are `BIGINT` and gaps don't matter.

**Countermeasure** None needed. Worth knowing precisely *because* it's
harmless — it tells you the dangerous crashes are elsewhere.

### C2 — Crash after the row, before sending

**Why** Row written with a blank outcome; the worker dies before the debit
leaves.

**Example** 18:00:01 row written `outcome = NULL`. 18:00:02 crash. Nothing sent.

**Affects** On restart the row looks *identical* to a crash after sending. The
system cannot tell them apart — and that's correct, because they genuinely are
indistinguishable.

**Countermeasure** Treat as `unknown` and reconcile. The bank says "never saw
it," back to `pending`, budget intact. A harmless situation handled with the
expensive path — correct, because you can't identify it as harmless without
asking.

### C3 — Crash after sending, before recording ⚠️

**Why** Debit sent, bank may have processed it, worker dies before recording.

**Example** 18:00:02 sent, ₹2,000 leaves the account. 18:00:03 crash. Nothing
recorded.

**Affects** If the row were written *after* firing, restart would find
**nothing**, believe the attempt never happened, and fire again. **₹4,000
charged.**

**Countermeasure** **Write-ahead.** The row goes in *before* the send, always.
C2 and C3 leave identical evidence, both become `unknown`, both reconcile — C2
returns "never arrived," C3 returns "debited." `store.NeedsReconciliation`
matches `fired_at IS NOT NULL AND outcome IS NULL` for exactly this reason —
a real crash never gets the chance to write `TIMEOUT` explicitly. Found
missing 2026-09-05 (the query only matched the explicit-TIMEOUT shape); see
`docs/JOURNAL.md`.

### C4 — The stalled worker ⚠️⚠️

The most important item in the project.

**Why** A worker takes the lease, sends the debit, then **freezes** — GC pause,
hypervisor pause, slow disk. Not dead, paused. The lease expires. Another
worker correctly takes over. Then the first wakes and continues as if nothing
happened.

**Example**

```
18:00:00  Worker A takes lease, fence 7
18:00:01  A sends debit — bank processes, ₹2,000 taken
18:00:02  A freezes
18:00:32  Lease expires
18:00:33  Worker B takes lease, fence 8. Fires. ₹2,000 taken AGAIN
18:00:35  A wakes. From A's view no time passed. It continues.
```

**Affects** Two debits. **Neither worker broke a rule.** And A cannot detect
this — being frozen isn't something a process experiences. Ask it "did you
stall?" and it honestly answers no.

**Countermeasure** **Fencing tokens.** Each lease carries a number that only
goes up. The bank remembers the highest seen per cycle and rejects anything
lower. A fires 7 → accepted. B fires 8 → accepted. A wakes, fires 7 → rejected.

**A doesn't know it's stale. B doesn't know A exists. Only the bank sees both.**
So the check must live at the bank, not in either worker. Same reason a Raft
leader steps down on seeing a higher term rather than noticing a partition.

### C5 — Two workers both think budget remains

**Why** Three attempts spent, one left. Two workers read `attempts_used = 3`
simultaneously. Both conclude one remains. Both fire.

**Example** `attempts_used` becomes 5. Both were correct when they read —
reading and writing were separate.

**Affects** Five attempts on a four-attempt limit. An NPCI violation.

**Countermeasure** **Compare-and-set.** Read a version, then update only if the
version hasn't changed. Zero rows returned means don't fire. Plus
`CHECK (attempts_used <= 4)` as a backstop.

### C6 — Two workers reconcile the same cycle

**Why** All three workers poll for `unknown` cycles. All three find the same
one.

**Example** All three ask the bank, all three hear "not debited," all three
move it to `pending`.

**Affects** The queries are reads, so no double-debit — but three workers
writing the same transition races, and the cycle could be scheduled twice.

**Countermeasure** **Reconciliation takes the lease too.** The rule becomes
uniform: *any operation that changes cycle state takes the lease first.*
5-minute duration, since asking with backoff can outlast 30 seconds.

### C7 — Budget spent with no attempt to show for it

**Why** Budget decremented, then the attempt row written. Two operations.
Crash between them.

**Example** `attempts_used: 1 → 2`, crash, row never written. That cycle
permanently lost 25% of its budget.

**Affects** Nothing errors. No exception, no log, no alert. The number is
quietly wrong forever, and only counting reveals it.

**Countermeasure** **One transaction** — budget, attempt and notice commit
together or not at all. Plus invariant 4, which counts. The only invariant that
compares *two facts against each other* rather than bounding one.

### C8 — Clock skew between workers

**Why** Worker A's clock runs 90 seconds ahead of B's. A thinks a lease
expired; B thinks it's held.

**Example** Lease expires 18:00:30. A's clock says 18:00:45 — "expired." B's
says 18:00:15 — "held." They disagree about the same fact.

**Affects** Two workers both believing they hold the same lease. Straight to C4.

**Countermeasure** **Use the database's clock.** Expiry is evaluated inside the
Postgres statement that acquires the lease. Not mitigated — **eliminated.**
There is only one clock in the system.

### C9 — Same failure event ingested twice

**Why** The upstream stream delivers a duplicate — a retry, a replay, a
partition healing.

**Example** `execution_id = xyz789` arrives twice. Two cycles created, or
budget spent twice.

**Affects** Duplicate work, corrupted counts, potentially a double-debit from
the front door.

**Countermeasure** Deduplicate on `execution_id`, **enforced by a unique
constraint in the database** — not in memory. In-memory dedup is lost on
restart, and restarts are exactly when replays arrive.

### C10 — Seq reused after an abandoned attempt is refunded

**Why** The next attempt's sequence number was computed as
`attempts_used + 1`. `attempts_used` is refundable — it drops back down
when an attempt is abandoned. `attempts` is append-only — the abandoned
row, and the idempotency key it claimed, never go away.

**Example** Attempt seq=1 abandoned, budget refunded, `attempts_used` back
to 0. Scheduler computes seq=1 again. `INSERT` collides with the still-live
row on `attempts_idempotency_key_key`. Retried every tick, forever, for
that cycle.

**Affects** Liveness, not safety — no double-debit, no corrupted row. But
the cycle can never schedule again, and the scheduler spends every tick
retrying an insert that structurally cannot succeed.

**Countermeasure** `seq` is now computed from `count(*) FROM attempts
WHERE cycle_id = $1` inside the same reservation transaction — a number
that only ever grows, unlike the refundable budget counter. See
`store.ReserveAttempt` and `docs/JOURNAL.md`, 2026-09-05.

### The pattern across C

| # | Failure | Fix |
|---|---|---|
| C1 | Crash before row | None — harmless |
| C2 | Crash before send | Reconcile |
| **C3** | **Crash after send** | **Write-ahead** |
| **C4** | **Stalled worker** | **Fencing tokens** |
| C5 | Both think budget left | Compare-and-set |
| C6 | Both reconcile | Lease on reconciliation |
| C7 | Silent budget burn | One transaction |
| C8 | Clock skew | Database's clock |
| C9 | Duplicate ingestion | Unique constraint |
| C10 | Seq reused after refund | Count, not refundable counter |

Three things worth holding:

**C3 and C4 are the only two that produce double-debits.** Everything else
corrupts state or wastes budget. These two charge a real person twice.

**The fixes have a shape.** C7 — don't order two writes carefully, make them
one. C8 — don't sync clocks, have one clock. C5 — don't check then write, check
*while* writing. The answer is never a guard; it's removing the situation that
made the bug possible.

**C4's fix is somewhere else entirely.** Every other fix is in our code. C4's
is at the bank, because the bank is the only participant that sees both
workers.

---

## D — Policy Violations

Nothing broke. No crash, no timeout, no race. The system ran perfectly and did
something it shouldn't have. Compliance and waste — **bad judgement, not bad
machinery.**

### D1 — Fired into a blocked window

**Why** NPCI blocks autopay ~10:00–13:00 IST. The scheduler picked a time
inside it.

**Example** Retry scheduled for the 8th at 10:30. Rahul has ₹12,000. Refused at
the door — not by his bank, by the rail.

**Affects** An attempt spent for nothing. The customer was ready and the
*timing* threw it away.

**Countermeasure** The constraint solver checks blocked windows before
returning a time: `10:30 → 18:00`. Plus invariant 5.

### D2 — Fired without the notice delivered

**Why** RBI requires 24 hours' notice. Scheduled for the 8th at 18:00, notice
never went out.

**Example** ₹2,000 taken with no warning. He didn't know it was coming.

**Affects** A regulatory violation, and a silent one — nothing errors, the
debit succeeds, and you find out when someone audits.

**Countermeasure** The notice row is written in the **same transaction** as the
budget reservation, so an attempt cannot exist without a notice queued. Before
firing, the worker **checks it was delivered**. Not delivered → abort, refund
budget, reschedule. This is the outbox pattern.

### D3 — Fired a fifth attempt

**Why** A bug in the budget arithmetic, or C5's race getting through.

**Example** `attempts_used` reaches 5. NPCI's limit is 4.

**Affects** Direct regulatory breach. The provider may throttle or flag.

**Countermeasure** Three layers: `attempts_used < 4` in the CAS `WHERE`,
`CHECK (attempts_used <= 4)` in the schema, invariant 2 as verification. The
schema constraint is the one no application bug can reach past.

### D4 — Spent an attempt on a hard decline

**Why** `MANDATE_REVOKED` came back and the system retried anyway. Classic "all
failures are retryable."

**Example** Revoked on the 10th. Retries the 12th, 14th, 16th. All fail
identically. Cycle closes. He was never told his mandate was broken.

**Affects** The entire cycle window spent on an action that cannot work.

**Countermeasure** Classification. Read the code, branch on the class. This is
the *entire value* of the failure taxonomy.

### D5 — Fired after the mandate was revoked

**Why** Attempt 3 scheduled for the 12th. On the 10th the customer revoked. The
scheduled attempt is still sitting there.

**Example** The 12th arrives. The worker fires against a mandate that no longer
exists.

**Affects** Not merely futile — non-compliant. Attempting to debit an account
we no longer have permission to touch.

**Countermeasure** **Re-validate at fire time, not just at schedule time.** Any
decision with a gap between deciding and doing needs re-checking at the doing.

### D6 — Scheduler picks up a cycle still in `unknown` ⚠️

**Why** The state machine forbids `unknown → in_flight`, so the *worker* can't
retry out of uncertainty. Nothing stops the **scheduler** from finding that
cycle and scheduling a fresh attempt.

**Example** Cycle `unknown`, reconciliation still running. Scheduler schedules
attempt 3. It fires. Reconciliation then reports the original *did* go through.
**Two debits — through the front door, state machine fully intact.**

**Affects** A double-debit that none of C's protections catch. The lease was
clean, the fence fresh, write-ahead worked. Everything did its job. The
scheduler simply shouldn't have been looking at that cycle.

**Countermeasure** `state = 'pending'` in the claim query, plus a
`NOT EXISTS` check against any attempt with `outcome IS NULL` or
`outcome = 'TIMEOUT'` — belt-and-suspenders, so a wrong `state` value alone
can't let a mid-flight cycle back in. See `store.SchedulableCycles` and
`docs/JOURNAL.md`, 2026-09-04.

> A state machine drawn on paper is a description. A state machine enforced in
> queries is a constraint. Drawing it doesn't implement it.

### D7 — A mandated retry slot leaves no room to deliver its own notice

**Why** Retry slots are anchored to the due date (`dueDate + 24h/72h/7d`).
The notice-lead requirement is anchored to *now* — the moment the retry
gets decided, which is whenever the previous attempt actually failed.
These two clocks can conflict: if a failure happens late enough in its own
day, the next mandated slot doesn't leave a full 24 hours from the failure
moment to deliver a fresh notice for it.

**Example** Original attempt fires and fails at 17:53 on the due date. The
T+24h slot lands at midnight the next day — under 24h away. The notice
deadline for that slot has already passed before the retry is even
scheduled. The worker correctly refuses to fire without proper notice,
abandons and refunds the attempt — and the scheduler picks the exact same
impossible slot again next tick. Not a crash, not a violation: a cycle
that can safely never make progress.

**Affects** Liveness for any cycle whose original attempt fails later in
its own due date — an ordinary case, not an edge case. Silent: no errors,
just a cycle stuck quietly re-abandoning the same slot forever.

**Countermeasure** `solve.First`/`solve.Next` now take `now` and never
return a `scheduledFor` less than `NoticeLead` (24h) after it. When the
mandated slot doesn't leave enough room, the fire time is pushed forward —
never earlier — just enough to make real delivery possible, then
re-checked against the blocked windows in case the push landed inside one.
The notice-lead rule itself is unchanged and un-weakened: every attempt
still needs its own fresh 24h notice, confirmed 2026-09-05. Only the
*slot-picking* was taught not to promise something the notice rule can
never keep. See `docs/JOURNAL.md`, 2026-09-05.

### The pattern across D

C failures announce themselves — a crash, a timeout, something visibly wrong.
**D failures are silent.** The debit succeeds, logs look clean, metrics look
fine. This is why the invariant queries earn their place: they're the only
thing that catches a system doing the wrong thing correctly.

---

## E — Recovery Harm

In A–D something goes wrong and you fix it. Here **the fixing is the damage.**

### E1 — Thundering herd on restart

**Why** Workers down two hours. The scheduler kept working, so attempts piled
up with times now passed. On restart every one is due at once.

**Example** 400 attempts overdue. All fire in seconds. The provider's rate
limit is 50/s. It refuses the excess with `TECHNICAL_DECLINE`. The system
treats that as a real attempt and increments `attempts_used` on all 400.

**Affects** 400 cycles just spent an attempt on nothing — the bank never
checked whether the money was there, it refused at the door because *we* sent
too fast. A quarter of the recovery budget across 400 customers, destroyed.

**During the outage nothing bad happened.** The damage occurred in the
recovery.

**Countermeasure** Rate limit the dispatch — token bucket capped below the
provider's limit. 400 attempts drain over 20 seconds instead of 400ms. Plus
jitter.

### E2 — Stale schedule fired late

**Why** An attempt scheduled for the 8th at 18:00 — a time chosen for
*reasons*. Predicted salary date. Avoiding the blocked window. It's now 20:30 on
the 9th.

**Example** Firing at 20:30 on the 9th ignores both reasons. It might land back
in a blocked window on a day the balance is spent.

**Affects** The attempt fires against reasoning that no longer holds. Not just
late — **wrong.**

**Countermeasure** If badly overdue, don't fire. Mark `ABANDONED_STALE`,
**refund the budget** (guarded on `fired_at IS NULL` — only if nothing was
sent), return to the scheduler to re-solve. Never delete the row.

> Catching up is not automatically the right move. A stale schedule is a wrong
> schedule, not merely a late one.

### E3 — Customer charged bounce fees for futile retries

**Why** On eNACH every failed debit incurs a bank penalty — ₹200–500 —
**charged to the customer.**

**Example** Rahul can't afford ₹2,000. Three retries. He now owes ₹900 in
bounce charges on top of the ₹2,000 he already couldn't pay.

**Affects** Real financial harm to someone already short, and collected
nothing. Plus the merchant relationship — he associates the gym with penalty
fees. **The one case where "maximize recovery" is actively the wrong
objective.**

**Countermeasure** Stopping rules. Three insufficient-funds in a row → stop,
escalate to a human contact or payment link instead of another debit. Treat
eNACH differently from UPI Autopay — same failure, different cost, different
threshold.

> A system with no stopping rules doesn't fail loudly. It becomes a harassment
> machine.

### E4 — Duplicate ingestion from a partition heal

**Why** A network partition heals and the upstream stream replays events it
wasn't sure we received.

**Example** 50 failure events replay. Without dedup, 50 cycles double-processed.

**Affects** The recovery of *another* system damages ours.

**Countermeasure** Unique constraint on `execution_id` in the database — not in
memory. Restarts lose in-memory dedup, and restarts are when replays arrive.

### E5 — Human resolution bypassing the guards

**Why** A cycle sits in `held`. An ops person resolves it with a manual
`UPDATE`.

**Example** `UPDATE mandate_cycles SET state = 'recovered' WHERE ...` — no
lease, no CAS, no audit entry, no version bump.

**Affects** Every protection bypassed at the exact moment accountability
matters most. The audit trail now has a hole precisely where a human made a
judgement call about money. And if a worker was mid-reconciliation, two writers
with no coordination.

**Countermeasure** Human actions go through the same store layer — same lease,
same CAS, same audit write. A CLI command, not raw SQL.

### The pattern across E

> The recovery is more dangerous than the outage.

Normally an outage is the problem and recovery is the fix. In all five, the
outage was harmless and the coming-back caused the damage.

**E3 changes the objective.** Everything else optimizes recovery. E3 says
sometimes the right answer is to collect *less*, because collecting more would
hurt someone already struggling. That is the argument for why *correct
disposition* is the goal rather than *maximum recovery*.
